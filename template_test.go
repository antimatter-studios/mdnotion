package main

import (
	"strings"
	"testing"
)

func TestApplyTemplate(t *testing.T) {
	params := map[string]string{"version": "v1.2", "parent": "Docs/InPace"}
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"no placeholders", "Docs/Installation", "Docs/Installation"},
		{"single placeholder", "{{version}}", "v1.2"},
		{"placeholder in path", "{{parent}}/{{version}}/Setup", "Docs/InPace/v1.2/Setup"},
		{"whitespace inside braces", "{{ version }}", "v1.2"},
		{"empty string", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := applyTemplate(tc.in, params)
			if err != nil {
				t.Fatalf("applyTemplate(%q) error: %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("applyTemplate(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestApplyTemplateMissingParam(t *testing.T) {
	_, err := applyTemplate("Docs/{{version}}", nil)
	if err == nil {
		t.Fatal("expected error for missing param, got nil")
	}
	if !strings.Contains(err.Error(), "version") {
		t.Errorf("error should name the missing param: %v", err)
	}
}

func TestParseParams(t *testing.T) {
	params, err := parseParams([]string{"version=v1.2", "parent=Docs/In=Pace"})
	if err != nil {
		t.Fatalf("parseParams error: %v", err)
	}
	if params["version"] != "v1.2" {
		t.Errorf("version = %q", params["version"])
	}
	if params["parent"] != "Docs/In=Pace" {
		t.Errorf("value containing '=' should be kept whole: %q", params["parent"])
	}

	for _, bad := range []string{"noequals", "=value"} {
		if _, err := parseParams([]string{bad}); err == nil {
			t.Errorf("parseParams(%q) expected error, got nil", bad)
		}
	}
}
