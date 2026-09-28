package main

import (
	"context"
	"fmt"
	"sort"

	"github.com/dstotijn/go-notion"
)

type orphanPage struct {
	id, title           string
	ancestors           []string
	containsPlannedPage bool
}

// findOrphanPages scans managed subtrees after every planned page has been
// resolved. Only pages bearing mdnotion's metadata can be considered orphans;
// hand-made pages and databases are never candidates for deletion.
func findOrphanPages(client *NotionClient, roots []node, plan []*planNode) ([]orphanPage, error) {
	planned := make(map[string]bool, len(plan))
	for _, entry := range plan {
		planned[entry.pageID] = true
	}
	visited := map[string]bool{}
	var orphans []orphanPage
	var walk func(id, title string, ancestors []string, root bool) (bool, error)
	walk = func(id, title string, ancestors []string, root bool) (bool, error) {
		if visited[id] {
			return planned[id], nil
		}
		visited[id] = true
		ctx := context.Background()
		cursor := ""
		managed := false
		type childPage struct{ id, title string }
		var children []childPage
		for {
			resp, err := client.NotionClient.FindBlockChildrenByID(ctx, id, &notion.PaginationQuery{StartCursor: cursor})
			if err != nil {
				return false, fmt.Errorf("listing children of page %s: %w", id, err)
			}
			for _, block := range resp.Results {
				switch b := block.(type) {
				case *notion.ChildPageBlock:
					children = append(children, childPage{b.ID(), b.Title})
				case notion.ChildPageBlock:
					children = append(children, childPage{b.ID(), b.Title})
				case *notion.CodeBlock:
					if _, found := parseMetaHash(richTextPlain(b.RichText)); found {
						managed = true
					}
				case notion.CodeBlock:
					if _, found := parseMetaHash(richTextPlain(b.RichText)); found {
						managed = true
					}
				}
			}
			if !resp.HasMore || resp.NextCursor == nil || *resp.NextCursor == "" {
				break
			}
			cursor = *resp.NextCursor
		}
		containsPlanned := planned[id]
		for _, child := range children {
			childPlanned, err := walk(child.id, child.title, append(append([]string(nil), ancestors...), id), false)
			if err != nil {
				return false, err
			}
			containsPlanned = containsPlanned || childPlanned
		}
		if !root && managed && !planned[id] {
			orphans = append(orphans, orphanPage{id: id, title: title, ancestors: ancestors, containsPlannedPage: containsPlanned})
		}
		return containsPlanned, nil
	}
	for _, root := range roots {
		if root.kind != kindPage || root.id == "" {
			continue
		}
		if _, err := walk(root.id, "", nil, true); err != nil {
			return nil, err
		}
	}
	sort.Slice(orphans, func(i, j int) bool {
		if orphans[i].title != orphans[j].title {
			return orphans[i].title < orphans[j].title
		}
		return orphans[i].id < orphans[j].id
	})
	return orphans, nil
}

// pruneOrphanPages archives topmost orphan subtrees only after publication
// succeeds. A former parent still containing a planned page is not safe to
// prune, even if its own metadata makes it an orphan.
func pruneOrphanPages(client *NotionClient, orphans []orphanPage) error {
	byID := make(map[string]orphanPage, len(orphans))
	for _, orphan := range orphans {
		if orphan.containsPlannedPage {
			return fmt.Errorf("cannot prune orphan %q (%s): it contains a planned page", orphan.title, orphan.id)
		}
		byID[orphan.id] = orphan
	}
	for _, orphan := range orphans {
		covered := false
		for _, ancestorID := range orphan.ancestors {
			if _, ok := byID[ancestorID]; ok {
				covered = true
				break
			}
		}
		if covered {
			continue
		}
		if _, err := client.NotionClient.DeleteBlock(context.Background(), orphan.id); err != nil {
			return fmt.Errorf("pruning orphan %q (%s): %w", orphan.title, orphan.id, err)
		}
		fmt.Printf("Pruned orphan: %s (%s)\n", orphan.title, notionPageURL(orphan.id))
	}
	return nil
}
