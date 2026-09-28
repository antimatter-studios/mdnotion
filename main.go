package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/pflag"

	"github.com/dstotijn/go-notion"
)

var (
	debugEnabled bool
	Version      = "dev"
)

func main() {
	var (
		token        string
		pageRef      string
		mdPath       string
		parentRef    string
		title        string
		manifestPath string
		paramFlags   []string
		appendF      bool
		replaceF     bool
		force        bool
		hashProperty string
		rewriteText  string
		dryRun       bool
		debugFlag    bool
		version      bool
	)
	pflag.StringVar(&token, "token", "", "Notion integration token")
	pflag.StringVar(&pageRef, "page", "", "Target Notion page: an ID or a title path like 'Wiki/Docs/Setup' (\\/ escapes a literal slash). Relative to --parent when both are given; must exist when given alone")
	pflag.StringVar(&mdPath, "md", "", "Path to markdown file")
	pflag.StringVar(&parentRef, "parent", "", "Page (ID or title path) below which the target is found or created. The parent itself must exist; everything below it may be created")
	pflag.StringVar(&title, "title", "", "Title of the page below --parent (default: the document's first H1, then the file name)")
	pflag.StringVar(&manifestPath, "manifest", "", "JSON manifest publishing a set of documents (see README)")
	pflag.StringArrayVar(&paramFlags, "param", nil, "Value for {{name}} placeholders, as name=value (repeatable)")
	pflag.BoolVar(&appendF, "append", false, "Append content to the bottom of the page instead of replacing it (non-idempotent: the content hash check does not apply)")
	pflag.BoolVar(&replaceF, "replace", false, "Replace the page content (the default; flag kept for compatibility)")
	pflag.BoolVar(&force, "force", false, "Publish even when the content hash says nothing changed")
	pflag.StringVar(&hashProperty, "hash-property", "", "Property name storing the content hash on database rows (default 'Content Hash')")
	pflag.StringVar(&rewriteText, "rewrite-text", "", "Path to JSON file mapping links to rewrite in the markdown file")
	pflag.BoolVar(&dryRun, "dry-run", false, "Run all logic except Notion sync (no API calls are made at all)")
	pflag.BoolVar(&debugFlag, "debug", false, "Enable debug output")
	pflag.BoolVarP(&version, "version", "v", false, "Print version and exit")
	pflag.Parse()

	if version {
		fmt.Println(Version)
		os.Exit(0)
	}

	debugEnabled = debugFlag

	debugLog("Given: \n--token '%s' \n--page '%s' \n--md '%s' \n--parent '%s' \n--title '%s' \n--manifest '%s' \n--param %v \n--append '%t' \n--force '%t' \n--hash-property '%s' \n--rewrite-text '%s'\n",
		token, pageRef, mdPath, parentRef, title, manifestPath, paramFlags, appendF, force, hashProperty, rewriteText)

	if appendF && replaceF {
		fatalUsage("Cannot use both --append and --replace flags at the same time.")
	}

	params, err := parseParams(paramFlags)
	if err != nil {
		fatal(err)
	}

	// {{param}} placeholders work in CLI references too, for scripting parity
	// with manifests.
	for _, ref := range []*string{&pageRef, &parentRef, &title} {
		if *ref, err = applyTemplate(*ref, params); err != nil {
			fatal(err)
		}
	}

	syncOpts := SyncOptions{
		Append:       appendF,
		Force:        force,
		HashProperty: hashProperty,
		DryRun:       dryRun,
	}

	// Manifest mode: publish a set of documents.
	if manifestPath != "" {
		if token == "" {
			fatalUsage("Manifest mode requires --token.")
		}
		if pageRef != "" || mdPath != "" || title != "" {
			fatalUsage("Cannot mix --manifest with --page/--md/--title.")
		}
		if appendF {
			fatalUsage("--append does not apply to manifests; manifest publishing replaces page content.")
		}
		notionClient := NewNotionClient(token)
		if err := runManifest(notionClient, ManifestOptions{
			Path:        manifestPath,
			Parent:      parentRef,
			Params:      params,
			RewriteText: rewriteText,
			Sync:        syncOpts,
		}); err != nil {
			fatal(err)
		}
		fmt.Println("✅ Manifest published successfully.")
		return
	}

	// Single-page mode: publish one document to one page.
	if token == "" || mdPath == "" || (pageRef == "" && parentRef == "") || len(os.Args) == 1 {
		pflag.Usage()
		os.Exit(1)
	}
	if pageRef != "" && parentRef != "" && looksLikePageID(pageRef) {
		fatalUsage("--page is an ID, which is already absolute; --parent does not apply.")
	}
	if title != "" && parentRef == "" {
		fatalUsage("--title only applies when creating below --parent.")
	}
	if title != "" && pageRef != "" {
		fatalUsage("--title conflicts with --page; a page's title is its identity.")
	}

	printAppTitle(mdPath, syncOpts)

	// Read the file contents
	mdContent, err := os.ReadFile(mdPath)
	if err != nil {
		fatal(fmt.Errorf("error reading markdown file: %w", err))
	}

	// Rewrite text if mapping is provided before conversion to notion blocks
	if rewriteText != "" {
		if mdContent, err = rewriteContent(mdContent, mdPath, rewriteText); err != nil {
			fatal(err)
		}
	}

	notionClient := NewNotionClient(token)

	pageTitlePath := title
	if pageTitlePath == "" {
		pageTitlePath = deriveTitle(mdPath, mdContent)
	}

	var target node
	switch {
	case dryRun:
		switch {
		case pageRef != "" && parentRef == "":
			fmt.Printf("[DRY RUN] Would resolve page '%s'\n", pageRef)
			target = node{id: placeholderPageID(pageRef)}
		case pageRef == "":
			fmt.Printf("[DRY RUN] Would resolve parent '%s' and find or create '%s'\n", parentRef, pageTitlePath)
			target = node{id: placeholderPageID(pageTitlePath)}
		default:
			fmt.Printf("[DRY RUN] Would resolve parent '%s' and find or create '%s'\n", parentRef, pageRef)
			target = node{id: placeholderPageID(pageRef)}
		}
	case pageRef != "" && parentRef == "":
		// Absolute target: the page must exist; nothing is ever created.
		if target, err = resolveRef(notionClient, pageRef); err != nil {
			fatal(err)
		}
	default:
		// Everything up to and including the parent must exist; everything
		// below it is found or created.
		parent, err := resolveRef(notionClient, parentRef)
		if err != nil {
			fatal(err)
		}
		path := pageRef
		if path == "" {
			path = pageTitlePath
		}
		if target, err = findOrCreatePath(notionClient, parent, path); err != nil {
			fatal(err)
		}
	}

	if err := syncPage(notionClient, target, mdPath, mdContent, syncOpts); err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	fmt.Println(err)
	os.Exit(1)
}

func fatalUsage(message string) {
	fmt.Println(message)
	os.Exit(1)
}

// rewriteContent applies rewrite-text mapping from a file to the markdown content.
func rewriteContent(mdContent []byte, mdPath, rewriteLink string) ([]byte, error) {
	data, err := os.ReadFile(rewriteLink)
	if err != nil {
		return nil, fmt.Errorf("Error reading rewrite-text mapping file: %w", err)
	}

	var singlePage map[string]string
	if err := json.Unmarshal(data, &singlePage); err == nil {
		debugLog("[DEBUG] Detected single-page rewrite mapping with %d links\n", len(singlePage))
		return []byte(rewriteTextMap(string(mdContent), singlePage)), nil
	}

	var multiPage map[string]map[string]string
	if err := json.Unmarshal(data, &multiPage); err == nil {
		debugLog("[DEBUG] Detected multi-page rewrite mapping. Searching for a matching page key in: %s\n", mdPath)
		var (
			matchedKey string
			pageMap    map[string]string
		)
		for key, candidate := range multiPage {
			if strings.Contains(mdPath, key) {
				matchedKey = key
				pageMap = candidate
				break
			}
		}
		if matchedKey != "" {
			debugLog("[DEBUG] Found %d links for page key '%s' (matched in: %s)\n", len(pageMap), matchedKey, mdPath)
			return []byte(rewriteTextMap(string(mdContent), pageMap)), nil
		}
		debugLog("[DEBUG] No mapping found for any key in '%s'. No rewrite applied.\n", mdPath)
		return mdContent, nil // no mapping for this page, return original content
	}
	debugLog("[DEBUG] Could not decode rewrite-text mapping file as single or multi-page mapping")
	return nil, fmt.Errorf("Error decoding rewrite-text mapping file as single or multi-page mapping")
}

// debugLog prints debug messages if debugEnabled is true.
func debugLog(format string, args ...interface{}) {
	if debugEnabled {
		fmt.Printf(format, args...)
	}
}

// mapLanguageToNotionCompatible maps common language identifiers to Notion-compatible values
func mapLanguageToNotionCompatible(lang string) string {
	langMap := map[string]string{
		"sh":   "shell",
		"bash": "bash",
		"zsh":  "shell",
		"js":   "javascript",
		"ts":   "typescript",
		"py":   "python",
		"rb":   "ruby",
		"cs":   "c#",
		"cpp":  "c++",
		"yml":  "yaml",
		"text": "plain text",
		"txt":  "plain text",
	}

	if mapped, ok := langMap[lang]; ok {
		return mapped
	}
	return lang
}

// rewriteTextMap replaces markdown links according to the mapping
func rewriteTextMap(content string, linkMap map[string]string) string {
	fmt.Printf("Rewriting %d links:\n", len(linkMap))
	for old, new := range linkMap {
		fmt.Printf("Replacing:  '%s' -> '%s'\n", old, new)
		// Replace text if present anywhere
		content = strings.ReplaceAll(content, old, new)
	}
	return content
}

// firstTitleBlock finds the first H1 in converted content. A fenced # line is
// a code block here, so it cannot accidentally become the page's identity.
func firstTitleBlock(blocks []notion.Block) (int, string) {
	for i, block := range blocks {
		if heading, ok := block.(notion.Heading1Block); ok {
			if title := strings.TrimSpace(richTextPlain(heading.RichText)); title != "" {
				return i, title
			}
		}
	}
	return -1, ""
}

// filterTitleBlock removes the same first H1 that deriveTitle uses for page identity.
func filterTitleBlock(blocks []notion.Block) (notion.Block, []notion.Block) {
	if i, _ := firstTitleBlock(blocks); i >= 0 {
		return blocks[i], append(blocks[:i:i], blocks[i+1:]...)
	}
	return nil, blocks
}

// processCodeBlock handles both pointer and non-pointer code blocks and returns a non-pointer type
func processCodeBlock(i int, codeBlock notion.CodeBlock) notion.CodeBlock {
	defaultLang := "plain text"
	if codeBlock.Language == nil {
		codeBlock.Language = &defaultLang
		fmt.Printf("⚠️  Fixed code block at index %d: set nil language to '%s'\n", i, defaultLang)
	} else {
		// Map language to Notion-compatible value
		originalLang := *codeBlock.Language
		mappedLang := mapLanguageToNotionCompatible(originalLang)
		if mappedLang != originalLang {
			*codeBlock.Language = mappedLang
			fmt.Printf("⚠️  Fixed code block at index %d: mapped language from '%s' to '%s'\n", i, originalLang, mappedLang)
		}
	}
	debugLog("⚠️  Code block at index %d has language: %s\n", i, *codeBlock.Language)
	return codeBlock
}

// validateContentBlocks scans and patches Notion blocks for known API problems (e.g., empty bulleted list items)
func validateContentBlocks(blocks []notion.Block) []notion.Block {
	var patched []notion.Block
	for i, block := range blocks {
		// Convert pointer to non-pointer type
		if _, ok := block.(*notion.CodeBlock); ok {
			block = notion.CodeBlock(*block.(*notion.CodeBlock))
		}

		switch b := block.(type) {
		case notion.BulletedListItemBlock:
			if len(b.RichText) == 0 || (len(b.RichText) == 1 && b.RichText[0].PlainText == "") {
				fmt.Printf("⚠️  Skipping empty bulleted list item at index %d\n", i)
				continue
			}
			if len(b.Children) > 0 {
				b.Children = validateContentBlocks(b.Children)
			}
			patched = append(patched, b)
		case notion.NumberedListItemBlock:
			if len(b.RichText) == 0 || (len(b.RichText) == 1 && b.RichText[0].PlainText == "") {
				fmt.Printf("⚠️  Skipping empty numbered list item at index %d\n", i)
				continue
			}
			if len(b.Children) > 0 {
				b.Children = validateContentBlocks(b.Children)
			}
			patched = append(patched, b)
		case notion.CodeBlock:
			b = processCodeBlock(i, b)
			patched = append(patched, b)
		default:
			// TODO: add more block type checks as needed
			patched = append(patched, block)
		}
	}
	return patched
}
