package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeManifestFixture writes a manifest plus any markdown files it needs and
// returns the manifest path.
func writeManifestFixture(t *testing.T, manifest string, mdFiles ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, md := range mdFiles {
		path := filepath.Join(dir, md)
		os.MkdirAll(filepath.Dir(path), 0o755)
		os.WriteFile(path, []byte("# "+strings.TrimSuffix(filepath.Base(md), ".md")+"\n\nbody\n"), 0o644)
	}
	path := filepath.Join(dir, "manifest.json")
	manifest = strings.ReplaceAll(manifest, "$DIR", dir)
	os.WriteFile(path, []byte(manifest), 0o644)
	return path
}

func TestLoadManifestNested(t *testing.T) {
	path := writeManifestFixture(t, `{
		"parent": "{{parent}}",
		"pages": [
			{ "md": "$DIR/install.md", "title": "Installation" },
			{ "title": "Guides", "children": [
				{ "md": "$DIR/build.md" }
			]}
		]
	}`, "install.md", "build.md")

	parent, plan, err := loadManifest(path, map[string]string{"parent": "Docs/InPace"})
	if err != nil {
		t.Fatalf("loadManifest error: %v", err)
	}
	if parent != "Docs/InPace" {
		t.Errorf("parent = %q", parent)
	}
	if len(plan) != 2 {
		t.Fatalf("expected 2 top-level nodes, got %d", len(plan))
	}
	if plan[0].title != "Installation" {
		t.Errorf("plan[0].title = %q", plan[0].title)
	}
	if len(plan[1].children) != 1 || plan[1].children[0].title != "build" {
		t.Errorf("nested child should derive title from H1: %#v", plan[1].children)
	}
	if !planNeedsParent(plan) {
		t.Error("plan with relative entries should need a parent")
	}
	docs := flattenPlan(plan)
	if len(docs) != 3 {
		t.Errorf("flattenPlan should include containers: got %d", len(docs))
	}
}

func TestLoadManifestRejectsSiblingTargetsBeforePublishing(t *testing.T) {
	for _, tc := range []struct {
		name, manifest string
	}{
		{"explicit titles", `{"parent":"Docs","pages":[{"md":"$DIR/a.md","title":"Same"},{"md":"$DIR/b.md","title":"Same"}]}`},
		{"derived titles", `{"parent":"Docs","pages":[{"md":"$DIR/a.md"},{"md":"$DIR/b.md"}]}`},
		{"absolute pages", `{"pages":[{"md":"$DIR/a.md","page":"Docs/Same"},{"md":"$DIR/b.md","page":"Docs/Same"}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := writeManifestFixture(t, tc.manifest, "a.md", "b.md")
			if tc.name == "derived titles" {
				for _, name := range []string{"a.md", "b.md"} {
					if err := os.WriteFile(filepath.Join(filepath.Dir(path), name), []byte("# Same\n"), 0o644); err != nil {
						t.Fatal(err)
					}
				}
			}
			_, _, err := loadManifest(path, nil)
			if err == nil || !strings.Contains(err.Error(), "a.md") || !strings.Contains(err.Error(), "b.md") {
				t.Fatalf("duplicate target error = %v", err)
			}
		})
	}
}

func TestLoadManifestAllowsSameTitleBelowDifferentParents(t *testing.T) {
	path := writeManifestFixture(t, `{"parent":"Docs","pages":[
		{"title":"First","children":[{"md":"$DIR/a.md","title":"Same"}]},
		{"title":"Second","children":[{"md":"$DIR/b.md","title":"Same"}]}
	]}`, "a.md", "b.md")
	if _, _, err := loadManifest(path, nil); err != nil {
		t.Fatalf("independent sibling scopes rejected: %v", err)
	}
}

func TestLoadManifestPageSet(t *testing.T) {
	path := writeManifestFixture(t, `{
		"pages": [
			{ "md": "$DIR/a.md", "page": "Engineering/Roadmap" },
			{ "md": "$DIR/b.md", "page": "25301aea292980c089d7e48bbed6365a" }
		]
	}`, "a.md", "b.md")

	parent, plan, err := loadManifest(path, nil)
	if err != nil {
		t.Fatalf("loadManifest error: %v", err)
	}
	if parent != "" {
		t.Errorf("parent = %q, want empty", parent)
	}
	if planNeedsParent(plan) {
		t.Error("page-set plan must not need a parent")
	}
}

func TestLoadManifestValidation(t *testing.T) {
	cases := []struct {
		name     string
		manifest string
		mdFiles  []string
		wantErr  string
	}{
		{
			"no pages",
			`{"pages": []}`,
			nil,
			"lists no pages",
		},
		{
			"neither md nor children",
			`{"pages": [{"title": "Empty"}]}`,
			nil,
			"neither 'md' nor 'children'",
		},
		{
			"nested page target",
			`{"pages": [{"title": "Top", "children": [{"md": "$DIR/a.md", "page": "Some/Where"}]}]}`,
			[]string{"a.md"},
			"top-level",
		},
		{
			"page with title",
			`{"pages": [{"md": "$DIR/a.md", "page": "Some/Where", "title": "X"}]}`,
			[]string{"a.md"},
			"'title' does not apply",
		},
		{
			"missing markdown file",
			`{"pages": [{"md": "$DIR/missing.md"}]}`,
			nil,
			"error reading markdown",
		},
		{
			"unfilled template",
			`{"pages": [{"md": "$DIR/a.md", "title": "{{version}}"}]}`,
			[]string{"a.md"},
			"{{version}}",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeManifestFixture(t, tc.manifest, tc.mdFiles...)
			_, _, err := loadManifest(path, nil)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error %q should contain %q", err, tc.wantErr)
			}
		})
	}
}

func TestDeriveTitle(t *testing.T) {
	cases := []struct {
		name    string
		mdPath  string
		content string
		want    string
	}{
		{"first H1", "docs/a.md", "intro\n\n# Real Title\n\n# Second\n", "Real Title"},
		{"H2 is not a title", "docs/installation.md", "## Section\n", "installation"},
		{"no heading falls back to file name", "docs/building.md", "plain text\n", "building"},
		{"empty path and no heading", "", "text\n", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := deriveTitle(tc.mdPath, []byte(tc.content)); got != tc.want {
				t.Errorf("deriveTitle = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRewriteSiblingLinks(t *testing.T) {
	docs := []*planNode{
		{md: "docs/configuration.md", pageID: "11111111111111111111111111111111"},
		{md: "docs/building.md", pageID: "22222222222222222222222222222222"},
		{md: "docs/sub/nested.md", pageID: "33333333333333333333333333333333"},
		{title: "Container with no md", pageID: "44444444444444444444444444444444"},
	}
	src := docs[0]
	content := strings.Join([]string{
		"see [Building](./building.md) and [Building again](building.md)",
		"nested: [Nested](sub/nested.md)",
		"anchored links are left alone: [B](./building.md#setup)",
		"external untouched: [R](../README.md)",
	}, "\n")

	got := string(rewriteSiblingLinks([]byte(content), src, docs))

	buildingURL := "(https://www.notion.so/22222222222222222222222222222222)"
	nestedURL := "(https://www.notion.so/33333333333333333333333333333333)"
	for _, want := range []string{
		"[Building]" + buildingURL,
		"[Building again]" + buildingURL,
		"[Nested]" + nestedURL,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("expected rewritten link %q in:\n%s", want, got)
		}
	}
	for _, untouched := range []string{"(./building.md#setup)", "(../README.md)"} {
		if !strings.Contains(got, untouched) {
			t.Errorf("expected %q to be left untouched in:\n%s", untouched, got)
		}
	}
}

func TestRewriteSiblingLinksAcrossDirectories(t *testing.T) {
	docs := []*planNode{
		{md: "docs/sub/nested.md", pageID: "33333333333333333333333333333333"},
		{md: "docs/building.md", pageID: "22222222222222222222222222222222"},
	}
	content := "up: [B](../building.md)"
	got := string(rewriteSiblingLinks([]byte(content), docs[0], docs))
	want := "[B](https://www.notion.so/22222222222222222222222222222222)"
	if !strings.Contains(got, want) {
		t.Errorf("expected %q in:\n%s", want, got)
	}
}

func TestNotionPageURLStripsDashes(t *testing.T) {
	got := notionPageURL("25301aea-2929-80c0-89d7-e48bbed6365a")
	want := "https://www.notion.so/25301aea292980c089d7e48bbed6365a"
	if got != want {
		t.Errorf("notionPageURL = %q, want %q", got, want)
	}
}

func TestParseMetaHash(t *testing.T) {
	hash, found := parseMetaHash(`{"mdnotion":{"content_hash":"abc123"}}`)
	if !found || hash != "abc123" {
		t.Errorf("parseMetaHash = (%q, %t)", hash, found)
	}
	for _, bad := range []string{
		"",
		"plain code block",
		`{"mdnotion":{}}`,
		`{"other":{"content_hash":"x"}}`,
		"not json but mentions mdnotion",
	} {
		if _, found := parseMetaHash(bad); found {
			t.Errorf("parseMetaHash(%q) should not match", bad)
		}
	}
}
