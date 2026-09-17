package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/dstotijn/go-notion"
)

// nodeKind distinguishes the two kinds of objects a Notion hierarchy is made
// of. The tool handles both transparently: callers only ever supply titles or
// IDs and the kind is discovered at runtime.
type nodeKind int

const (
	kindPage nodeKind = iota
	kindDatabase
)

func (k nodeKind) String() string {
	if k == kindDatabase {
		return "database"
	}
	return "page"
}

// node is a resolved position in the Notion hierarchy.
type node struct {
	id   string
	kind nodeKind
	// row is true for pages that are rows of a database. Rows have custom
	// properties (used for hash storage); plain pages do not.
	row bool
}

// nodeFromID resolves a bare ID to a node, probing whether it is a page or a
// database.
func (c *NotionClient) nodeFromID(id string) (node, error) {
	ctx := context.Background()
	page, pageErr := c.NotionClient.FindPageByID(ctx, id)
	if pageErr == nil {
		return node{id: page.ID, kind: kindPage, row: page.Parent.Type == notion.ParentTypeDatabase}, nil
	}
	if _, dbErr := c.NotionClient.FindDatabaseByID(ctx, id); dbErr == nil {
		return node{id: id, kind: kindDatabase}, nil
	}
	return node{}, fmt.Errorf("ID '%s' is neither a page nor a database visible to this integration: %v", id, pageErr)
}

// searchNodesByTitle returns all pages and databases shared with the
// integration whose title matches exactly.
func (c *NotionClient) searchNodesByTitle(title string) ([]node, error) {
	ctx := context.Background()
	var matches []node
	cursor := ""
	for {
		resp, err := c.NotionClient.Search(ctx, &notion.SearchOpts{
			Query:       title,
			StartCursor: cursor,
		})
		if err != nil {
			return nil, err
		}
		for _, result := range resp.Results {
			switch obj := result.(type) {
			case notion.Page:
				if pageTitle(obj) == title {
					matches = append(matches, node{id: obj.ID, kind: kindPage, row: obj.Parent.Type == notion.ParentTypeDatabase})
				}
			case notion.Database:
				if richTextPlain(obj.Title) == title {
					matches = append(matches, node{id: obj.ID, kind: kindDatabase})
				}
			}
		}
		if !resp.HasMore || resp.NextCursor == nil || *resp.NextCursor == "" {
			break
		}
		cursor = *resp.NextCursor
	}
	return matches, nil
}

// findChildByTitle finds the child of parent with the given title. Page
// parents are enumerated through their child blocks (which surface both child
// pages and child databases); database parents are enumerated through their
// rows. Returns found=false if no child matches; more than one match is an
// error, never a guess.
func (c *NotionClient) findChildByTitle(parent node, title string) (node, bool, error) {
	var matches []node
	var err error
	if parent.kind == kindDatabase {
		matches, err = c.databaseRowsByTitle(parent.id, title)
	} else {
		matches, err = c.pageChildrenByTitle(parent.id, title)
	}
	if err != nil {
		return node{}, false, err
	}
	switch len(matches) {
	case 0:
		return node{}, false, nil
	case 1:
		return matches[0], true, nil
	default:
		return node{}, false, fmt.Errorf("title '%s' is ambiguous below %s %s: %d children match", title, parent.kind, parent.id, len(matches))
	}
}

func (c *NotionClient) pageChildrenByTitle(pageID, title string) ([]node, error) {
	ctx := context.Background()
	var matches []node
	cursor := ""
	for {
		resp, err := c.NotionClient.FindBlockChildrenByID(ctx, pageID, &notion.PaginationQuery{StartCursor: cursor})
		if err != nil {
			return nil, fmt.Errorf("failed to list children of page %s: %w", pageID, err)
		}
		for _, block := range resp.Results {
			switch b := block.(type) {
			case *notion.ChildPageBlock:
				if b.Title == title {
					matches = append(matches, node{id: b.ID(), kind: kindPage})
				}
			case notion.ChildPageBlock:
				if b.Title == title {
					matches = append(matches, node{id: b.ID(), kind: kindPage})
				}
			case *notion.ChildDatabaseBlock:
				if b.Title == title {
					matches = append(matches, node{id: b.ID(), kind: kindDatabase})
				}
			case notion.ChildDatabaseBlock:
				if b.Title == title {
					matches = append(matches, node{id: b.ID(), kind: kindDatabase})
				}
			}
		}
		if !resp.HasMore || resp.NextCursor == nil || *resp.NextCursor == "" {
			break
		}
		cursor = *resp.NextCursor
	}
	return matches, nil
}

func (c *NotionClient) databaseRowsByTitle(databaseID, title string) ([]node, error) {
	ctx := context.Background()
	var matches []node
	cursor := ""
	for {
		resp, err := c.NotionClient.QueryDatabase(ctx, databaseID, &notion.DatabaseQuery{StartCursor: cursor})
		if err != nil {
			return nil, fmt.Errorf("failed to query database %s: %w", databaseID, err)
		}
		for _, row := range resp.Results {
			if pageTitle(row) == title {
				matches = append(matches, node{id: row.ID, kind: kindPage, row: true})
			}
		}
		if !resp.HasMore || resp.NextCursor == nil || *resp.NextCursor == "" {
			break
		}
		cursor = *resp.NextCursor
	}
	return matches, nil
}

// createChild creates a child with the given title below parent: a plain page
// below a page, a row below a database. The tool never creates databases —
// a database requires a schema, which is the caller's business, not ours.
func (c *NotionClient) createChild(parent node, title string) (node, error) {
	ctx := context.Background()
	params := notion.CreatePageParams{ParentID: parent.id}
	row := false
	if parent.kind == kindDatabase {
		titleProp, err := c.databaseTitleProperty(parent.id)
		if err != nil {
			return node{}, err
		}
		params.ParentType = notion.ParentTypeDatabase
		params.DatabasePageProperties = &notion.DatabasePageProperties{
			titleProp: notion.DatabasePageProperty{
				Title: []notion.RichText{{Text: &notion.Text{Content: title}}},
			},
		}
		row = true
	} else {
		params.ParentType = notion.ParentTypePage
		params.Title = []notion.RichText{{Text: &notion.Text{Content: title}}}
	}
	page, err := c.NotionClient.CreatePage(ctx, params)
	if err != nil {
		return node{}, fmt.Errorf("failed to create page '%s' under %s %s: %w", title, parent.kind, parent.id, err)
	}
	return node{id: page.ID, kind: kindPage, row: row}, nil
}

// databaseTitleProperty returns the name of a database's title property.
func (c *NotionClient) databaseTitleProperty(databaseID string) (string, error) {
	ctx := context.Background()
	db, err := c.NotionClient.FindDatabaseByID(ctx, databaseID)
	if err != nil {
		return "", fmt.Errorf("failed to fetch database %s: %w", databaseID, err)
	}
	for name, prop := range db.Properties {
		if prop.Type == "title" {
			return name, nil
		}
	}
	return "", fmt.Errorf("database %s has no title property", databaseID)
}

// pageTitle extracts the plain-text title of a page, regardless of whether it
// is a plain page or a database row.
func pageTitle(page notion.Page) string {
	switch props := page.Properties.(type) {
	case notion.PageProperties:
		return richTextPlain(props.Title.Title)
	case notion.DatabasePageProperties:
		for _, prop := range props {
			if prop.Type == "title" {
				return richTextPlain(prop.Title)
			}
		}
	}
	return ""
}

func richTextPlain(richText []notion.RichText) string {
	var sb strings.Builder
	for _, t := range richText {
		sb.WriteString(t.PlainText)
	}
	return sb.String()
}
