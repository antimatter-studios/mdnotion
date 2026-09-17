package main

import (
	"fmt"
	"regexp"
	"strings"
)

var placeholderPattern = regexp.MustCompile(`\{\{\s*([A-Za-z0-9_.-]+)\s*\}\}`)

// parseParams parses repeated --param name=value flags into a map.
func parseParams(args []string) (map[string]string, error) {
	params := make(map[string]string, len(args))
	for _, arg := range args {
		name, value, found := strings.Cut(arg, "=")
		if !found || strings.TrimSpace(name) == "" {
			return nil, fmt.Errorf("invalid --param '%s': expected name=value", arg)
		}
		params[name] = value
	}
	return params, nil
}

// applyTemplate substitutes {{name}} placeholders in s from params. Any
// placeholder left without a value is an error — a half-rendered reference
// must never reach Notion.
func applyTemplate(s string, params map[string]string) (string, error) {
	var missing []string
	result := placeholderPattern.ReplaceAllStringFunc(s, func(match string) string {
		name := placeholderPattern.FindStringSubmatch(match)[1]
		if value, ok := params[name]; ok {
			return value
		}
		missing = append(missing, name)
		return match
	})
	if len(missing) > 0 {
		return "", fmt.Errorf("no value given for {{%s}} (use --param %s=...)", missing[0], missing[0])
	}
	return result, nil
}
