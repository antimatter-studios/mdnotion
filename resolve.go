package main

import (
	"fmt"
	"regexp"
	"strings"
)

var pageIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{32}$`)

// looksLikePageID reports whether s is a Notion ID (32 hex chars, dashes
// optional) as opposed to a title path.
func looksLikePageID(s string) bool {
	return pageIDPattern.MatchString(strings.ReplaceAll(s, "-", ""))
}

// splitTitlePath splits a title path on '/', treating '\/' as a literal slash
// inside a title. Any other backslash is kept verbatim.
func splitTitlePath(path string) []string {
	var segments []string
	var current strings.Builder
	runes := []rune(path)
	for i := 0; i < len(runes); i++ {
		switch {
		case runes[i] == '\\' && i+1 < len(runes) && runes[i+1] == '/':
			current.WriteRune('/')
			i++
		case runes[i] == '/':
			segments = append(segments, current.String())
			current.Reset()
		default:
			current.WriteRune(runes[i])
		}
	}
	return append(segments, current.String())
}

// validateTitlePath returns the segments of path, or an error if any segment
// is empty.
func validateTitlePath(path string) ([]string, error) {
	segments := splitTitlePath(path)
	for _, segment := range segments {
		if strings.TrimSpace(segment) == "" {
			return nil, fmt.Errorf("title path '%s' contains an empty segment", path)
		}
	}
	return segments, nil
}

// resolveRef resolves a page reference — an ID or a title path — to a node.
// Resolution is read-only and never creates anything: a missing or ambiguous
// segment is an error. The first segment of a title path is found by a
// workspace search; later segments by walking children (pages and databases
// alike).
func resolveRef(client *NotionClient, ref string) (node, error) {
	if looksLikePageID(ref) {
		return client.nodeFromID(ref)
	}

	segments, err := validateTitlePath(ref)
	if err != nil {
		return node{}, err
	}

	matches, err := client.searchNodesByTitle(segments[0])
	if err != nil {
		return node{}, fmt.Errorf("searching for '%s': %w", segments[0], err)
	}
	if len(matches) == 0 {
		return node{}, fmt.Errorf("no page or database titled '%s' is shared with this integration", segments[0])
	}
	if len(matches) > 1 {
		ids := make([]string, len(matches))
		for i, m := range matches {
			ids[i] = fmt.Sprintf("%s %s", m.kind, m.id)
		}
		return node{}, fmt.Errorf("title '%s' is ambiguous (%d matches: %s); use an ID instead", segments[0], len(matches), strings.Join(ids, ", "))
	}

	current := matches[0]
	resolved := segments[0]
	for _, title := range segments[1:] {
		child, found, err := client.findChildByTitle(current, title)
		if err != nil {
			return node{}, fmt.Errorf("resolving '%s' under '%s': %w", title, resolved, err)
		}
		if !found {
			return node{}, fmt.Errorf("could not resolve '%s' under '%s'", title, resolved)
		}
		current = child
		resolved += "/" + title
	}
	return current, nil
}

// findOrCreatePath walks a title path below parent, creating missing pages
// (plain pages below pages, rows below databases — never databases), and
// returns the final node.
func findOrCreatePath(client *NotionClient, parent node, path string) (node, error) {
	segments, err := validateTitlePath(path)
	if err != nil {
		return node{}, err
	}
	current := parent
	for _, title := range segments {
		child, found, err := client.findChildByTitle(current, title)
		if err != nil {
			return node{}, err
		}
		if !found {
			child, err = client.createChild(current, title)
			if err != nil {
				return node{}, err
			}
			fmt.Printf("Created page '%s' (%s)\n", title, child.id)
		}
		current = child
	}
	return current, nil
}
