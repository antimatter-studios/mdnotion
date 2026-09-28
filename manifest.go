package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/brittonhayes/notionmd"
)

// Manifest is a nested description of which markdown files go to which Notion
// pages. Nesting expresses hierarchy: children are published below their
// containing entry's page. The optional parent anchors all top-level entries
// that don't carry an absolute "page" target of their own.
type Manifest struct {
	Parent string  `json:"parent,omitempty"`
	Pages  []Entry `json:"pages"`
}

// Entry is one node of the manifest.
//
//   - MD: markdown file providing the page's content. Optional for container
//     entries that only exist to hold children.
//   - Title: the page's title and its identity below its parent — pages are
//     matched by title on re-runs. Optional when MD is set (falls back to the
//     document's first H1, then the file name). May be a path
//     ("Guides/Installation") to create intermediate pages.
//   - Page: an absolute target (ID or title path) for top-level entries that
//     update an existing page wherever it lives, independent of the parent.
//   - Children: entries published below this entry's page.
//
// All string fields may contain {{param}} placeholders, filled from --param.
type Entry struct {
	MD       string  `json:"md,omitempty"`
	Title    string  `json:"title,omitempty"`
	Page     string  `json:"page,omitempty"`
	Children []Entry `json:"children,omitempty"`
}

// planNode is a validated, template-rendered, content-loaded manifest entry.
type planNode struct {
	md       string
	title    string
	page     string
	content  []byte
	children []*planNode

	target node   // filled in during execution
	pageID string // target.id, or a placeholder in dry-run
}

// loadManifest reads, template-renders, validates, and content-loads a
// manifest file into a plan.
func loadManifest(path string, params map[string]string) (parentRef string, plan []*planNode, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", nil, fmt.Errorf("error reading manifest file: %w", err)
	}
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return "", nil, fmt.Errorf("error decoding manifest file '%s': %w", path, err)
	}
	if len(manifest.Pages) == 0 {
		return "", nil, fmt.Errorf("manifest '%s' lists no pages", path)
	}
	if parentRef, err = applyTemplate(manifest.Parent, params); err != nil {
		return "", nil, err
	}
	plan, err = buildPlan(manifest.Pages, params, 0)
	if err != nil {
		return "", nil, err
	}
	return parentRef, plan, nil
}

// buildPlan validates entries and resolves templates, titles, and content.
func buildPlan(entries []Entry, params map[string]string, depth int) ([]*planNode, error) {
	plan := make([]*planNode, 0, len(entries))
	for i, entry := range entries {
		var n planNode
		var err error
		if n.md, err = applyTemplate(entry.MD, params); err != nil {
			return nil, err
		}
		if n.title, err = applyTemplate(entry.Title, params); err != nil {
			return nil, err
		}
		if n.page, err = applyTemplate(entry.Page, params); err != nil {
			return nil, err
		}

		if n.md == "" && len(entry.Children) == 0 {
			return nil, fmt.Errorf("manifest entry %d has neither 'md' nor 'children'", i)
		}
		if n.page != "" && depth > 0 {
			return nil, fmt.Errorf("manifest entry '%s': absolute 'page' targets are only allowed on top-level entries; nested entries are located by their position", n.page)
		}
		if n.page != "" && n.title != "" {
			return nil, fmt.Errorf("manifest entry '%s': 'page' targets an existing page, so 'title' does not apply", n.page)
		}

		if n.md != "" {
			if n.content, err = os.ReadFile(n.md); err != nil {
				return nil, fmt.Errorf("error reading markdown file '%s': %w", n.md, err)
			}
		}
		if n.title == "" && n.page == "" {
			n.title = deriveTitle(n.md, n.content)
			if n.title == "" {
				return nil, fmt.Errorf("manifest entry %d needs a 'title' (no markdown heading to derive one from)", i)
			}
		}

		if n.children, err = buildPlan(entry.Children, params, depth+1); err != nil {
			return nil, err
		}
		plan = append(plan, &n)
	}
	return plan, nil
}

// planNeedsParent reports whether any top-level entry is positioned relative
// to the manifest parent (rather than an absolute 'page' target).
func planNeedsParent(plan []*planNode) bool {
	for _, n := range plan {
		if n.page == "" {
			return true
		}
	}
	return false
}

// flattenPlan returns every node in the plan, depth first.
func flattenPlan(plan []*planNode) []*planNode {
	var all []*planNode
	for _, n := range plan {
		all = append(all, n)
		all = append(all, flattenPlan(n.children)...)
	}
	return all
}

// ManifestOptions are the inputs of one manifest run.
type ManifestOptions struct {
	Path        string
	Parent      string // CLI --parent, used when the manifest has no parent
	Params      map[string]string
	RewriteText string
	Sync        SyncOptions
}

// runManifest publishes a manifest: phase 1 resolves or creates every page,
// phase 2 rewrites links and publishes content. Dry-run prints the plan and
// makes no API calls at all.
func runManifest(client *NotionClient, opts ManifestOptions) error {
	parentRef, plan, err := loadManifest(opts.Path, opts.Params)
	if err != nil {
		return err
	}

	if parentRef != "" && opts.Parent != "" {
		return fmt.Errorf("the manifest defines a parent ('%s') and --parent is also given ('%s'); use one", parentRef, opts.Parent)
	}
	if parentRef == "" {
		parentRef = opts.Parent
	}
	if planNeedsParent(plan) && parentRef == "" {
		return fmt.Errorf("manifest entries without a 'page' target need a parent: set \"parent\" in the manifest or pass --parent")
	}

	// Phase 1: resolve every entry to a page. Entries with an absolute 'page'
	// target are resolved read-only; everything else is found or created below
	// its position. Dry-run prints the plan instead.
	if opts.Sync.DryRun {
		if planNeedsParent(plan) {
			fmt.Printf("[DRY RUN] Would resolve parent '%s'\n", parentRef)
		}
		printPlan(plan, 1)
	} else {
		var parent node
		if planNeedsParent(plan) {
			if parent, err = resolveRef(client, parentRef); err != nil {
				return err
			}
		}
		if err := executePlan(client, plan, parent); err != nil {
			return err
		}
	}

	// Phase 2: rewrite links and publish content into each page.
	docs := flattenPlan(plan)
	for _, doc := range docs {
		if doc.md == "" {
			continue
		}
		printAppTitle(doc.md, opts.Sync)

		content := doc.content
		if opts.RewriteText != "" {
			if content, err = rewriteContent(content, doc.md, opts.RewriteText); err != nil {
				return err
			}
		}
		content = rewriteSiblingLinks(content, doc, docs)

		if err := syncPage(client, doc.target, doc.md, content, opts.Sync); err != nil {
			return fmt.Errorf("error publishing '%s': %w", doc.md, err)
		}
	}

	return nil
}

func executePlan(client *NotionClient, plan []*planNode, parent node) error {
	for _, n := range plan {
		var err error
		if n.page != "" {
			if n.target, err = resolveRef(client, n.page); err != nil {
				return err
			}
		} else {
			if n.target, err = findOrCreatePath(client, parent, n.title); err != nil {
				return err
			}
		}
		n.pageID = n.target.id
		if err := executePlan(client, n.children, n.target); err != nil {
			return err
		}
	}
	return nil
}

func printPlan(plan []*planNode, depth int) {
	indent := strings.Repeat("  ", depth)
	for _, n := range plan {
		source := ""
		if n.md != "" {
			source = " <- " + n.md
		}
		if n.page != "" {
			fmt.Printf("[DRY RUN]%sWould resolve page '%s'%s\n", indent, n.page, source)
			n.pageID = placeholderPageID(n.page)
		} else {
			fmt.Printf("[DRY RUN]%sWould find or create '%s'%s\n", indent, n.title, source)
			n.pageID = placeholderPageID(n.title)
		}
		printPlan(n.children, depth+1)
	}
}

// deriveTitle returns the first converted H1, else the markdown file name,
// else "". Using the same blocks as publication keeps fenced # comments out
// of page identity and agrees with filterTitleBlock about which H1 is removed.
func deriveTitle(mdPath string, content []byte) string {
	if blocks, err := notionmd.Convert(string(content)); err == nil {
		_, title := firstTitleBlock(blocks)
		if title != "" {
			return title
		}
	}
	if mdPath == "" {
		return ""
	}
	base := filepath.Base(mdPath)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

// rewriteSiblingLinks replaces relative markdown links from src to other
// documents of the same manifest with the target's Notion page URL, e.g.
// "(./building.md)" with "(https://www.notion.so/<id>)". The page IDs only
// exist once phase 1 has run, which is why this cannot be expressed in a
// static --rewrite-text mapping.
func rewriteSiblingLinks(content []byte, src *planNode, docs []*planNode) []byte {
	text := string(content)
	srcDir := filepath.Dir(src.md)
	for _, target := range docs {
		if target == src || target.md == "" {
			continue
		}
		rel, err := filepath.Rel(srcDir, target.md)
		if err != nil {
			continue
		}
		url := notionPageURL(target.pageID)
		text = strings.ReplaceAll(text, "("+rel+")", "("+url+")")
		if !strings.HasPrefix(rel, "../") {
			text = strings.ReplaceAll(text, "(./"+rel+")", "("+url+")")
		}
	}
	return []byte(text)
}

// notionPageURL builds the permalink of a page from its ID. The title slug
// seen in browser URLs is optional: Notion resolves by the trailing ID alone.
func notionPageURL(pageID string) string {
	return "https://www.notion.so/" + strings.ReplaceAll(pageID, "-", "")
}

// placeholderPageID is the stand-in page ID used in dry-run mode, where no
// pages are created or looked up.
func placeholderPageID(title string) string {
	return "dry-run-" + strings.ReplaceAll(title, " ", "-")
}
