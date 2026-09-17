package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/brittonhayes/notionmd"
	"github.com/dstotijn/go-notion"
)

// SyncOptions controls how syncPage publishes a document to a page.
type SyncOptions struct {
	// Append adds the content to the bottom of the page instead of replacing
	// it. Appending is inherently non-idempotent, so the content hash check
	// does not apply to it.
	Append bool
	// Force publishes even when the content hash says nothing changed.
	Force        bool
	HashProperty string
	DryRun       bool
}

// metaMarker keys the metadata code block used to store the content hash on
// plain pages (which, unlike database rows, have no custom properties).
const metaMarker = "mdnotion"

type pageMeta struct {
	ContentHash string `json:"content_hash"`
}

// syncPage converts markdown content to Notion blocks and publishes them to
// the target page. Unless forced or appending, a page whose stored content
// hash matches the new content is left completely untouched. In dry-run mode
// the full conversion runs but no Notion API calls are made at all.
func syncPage(client *NotionClient, target node, mdPath string, mdContent []byte, opts SyncOptions) error {
	blocks, err := notionmd.Convert(string(mdContent))
	if err != nil {
		return fmt.Errorf("error converting markdown to Notion blocks: %w", err)
	}

	// Image processing uploads files to Notion, so it is skipped in dry-run.
	if !opts.DryRun {
		blocks, err = ProcessImageBlocks(blocks, mdPath, client)
		if err != nil {
			return fmt.Errorf("failed to process images: %w", err)
		}
	}

	debugBlocks(blocks)

	hashBytes := sha256.Sum256(mdContent)
	contentHash := fmt.Sprintf("%x", hashBytes[:])

	blocks = validateContentBlocks(blocks)
	// The leading H1 is dropped: the page already shows its title, and the
	// title is the page's identity, so it is never renamed from content.
	_, blocks = filterTitleBlock(blocks)

	if opts.DryRun {
		fmt.Println("[DRY RUN] All parsing, conversion, and hash logic completed. No changes made to Notion.")
		fmt.Printf("[MD CONTENT]\n%s\n\n", mdContent)
		return nil
	}

	// The content hash makes re-publishing idempotent: an unchanged document
	// must not touch the page at all (no history churn, no edit timestamps,
	// no notifications). Database rows store the hash in a page property;
	// plain pages store it in a metadata code block at the end of the page.
	if !opts.Append {
		storedHash, err := readContentHash(client, target, opts.HashProperty)
		if err != nil {
			return err
		}
		debugLog("Stored hash:  %s\nContent hash: %s\n", storedHash, contentHash)
		if storedHash == contentHash && !opts.Force {
			fmt.Println("⚠️ No content change detected. Skipping update.")
			return nil
		}

		if err := client.ClearPageContent(target.id); err != nil {
			return fmt.Errorf("error clearing Notion page: %w", err)
		}
	}

	if !opts.Append && !target.row {
		blocks = append(blocks, metaBlock(contentHash))
	}

	if err := client.AddPageContent(target.id, blocks); err != nil {
		return fmt.Errorf("error updating Notion page: %w", err)
	}

	if !opts.Append && target.row {
		if err := client.SetProperty(target.id, hashPropertyName(opts.HashProperty), contentHash); err != nil {
			fmt.Printf("Warning: failed to store content hash: %s\n", err)
		}
	}

	fmt.Println("✅ Page updated successfully.")
	return nil
}

func hashPropertyName(override string) string {
	if override != "" {
		return override
	}
	return "Content Hash"
}

// readContentHash reads the hash stored by the previous publish, from the
// page property (database rows) or the metadata block (plain pages). A page
// never published by this tool yields "".
func readContentHash(client *NotionClient, target node, hashProperty string) (string, error) {
	if target.row {
		hash, err := client.GetProperty(target.id, hashPropertyName(hashProperty))
		if err != nil {
			return "", fmt.Errorf("error reading '%s' property: %w", hashPropertyName(hashProperty), err)
		}
		return hash, nil
	}
	return client.readMetaHash(target.id)
}

// metaBlock builds the metadata code block appended to plain pages.
func metaBlock(contentHash string) notion.Block {
	payload, _ := json.Marshal(map[string]pageMeta{metaMarker: {ContentHash: contentHash}})
	language := "json"
	return notion.CodeBlock{
		RichText: []notion.RichText{{Text: &notion.Text{Content: string(payload)}}},
		Caption:  []notion.RichText{{Text: &notion.Text{Content: "mdnotion metadata — do not edit"}}},
		Language: &language,
	}
}

// readMetaHash scans a page's blocks for the metadata code block and returns
// the stored content hash, or "" if the page has none.
func (c *NotionClient) readMetaHash(pageID string) (string, error) {
	ctx := context.Background()
	cursor := ""
	for {
		resp, err := c.NotionClient.FindBlockChildrenByID(ctx, pageID, &notion.PaginationQuery{StartCursor: cursor})
		if err != nil {
			return "", fmt.Errorf("failed to list children of page %s: %w", pageID, err)
		}
		for _, block := range resp.Results {
			var richText []notion.RichText
			switch b := block.(type) {
			case *notion.CodeBlock:
				richText = b.RichText
			case notion.CodeBlock:
				richText = b.RichText
			default:
				continue
			}
			if hash, found := parseMetaHash(richTextPlain(richText)); found {
				return hash, nil
			}
		}
		if !resp.HasMore || resp.NextCursor == nil || *resp.NextCursor == "" {
			break
		}
		cursor = *resp.NextCursor
	}
	return "", nil
}

// parseMetaHash extracts the content hash from a metadata code block's text.
func parseMetaHash(text string) (string, bool) {
	if !strings.Contains(text, metaMarker) {
		return "", false
	}
	var payload map[string]pageMeta
	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		return "", false
	}
	meta, ok := payload[metaMarker]
	if !ok || meta.ContentHash == "" {
		return "", false
	}
	return meta.ContentHash, true
}

// printAppTitle prints a detailed operation title for one document.
func printAppTitle(mdPath string, opts SyncOptions) {
	mode := "replace"
	if opts.Append {
		mode = "append"
	}
	details := []string{"mdnotion: Processing file '" + mdPath + "' using " + mode}
	if opts.Force {
		details = append(details, "forced")
	}
	fmt.Println("\n===== " + strings.Join(details, ", ") + " =====")
	fmt.Println()
}

func debugBlocks(blocks []notion.Block) {
	// early dropout
	if !debugEnabled {
		return
	}

	debugLog("Found %d blocks after markdown conversion\n", len(blocks))
	for i, block := range blocks {
		debugLog("Block %d is of type: %T\n", i, block)

		// Check specifically for code blocks
		if codeBlock, ok := block.(*notion.CodeBlock); ok {
			debugLog("Found code block at index %d\n", i)
			if codeBlock.Language == nil {
				debugLog("  Language is nil\n")
			} else {
				debugLog("  Language is '%s'\n", *codeBlock.Language)
			}
		}
	}
}
