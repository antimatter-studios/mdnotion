package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRewriteContentChoosesLongestPageKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "links.json")
	data := `{"setup.md":{"TOKEN":"short"},"docs/advanced-setup.md":{"TOKEN":"specific"}}`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		got, err := rewriteContent([]byte("TOKEN"), "docs/advanced-setup.md", path)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != "specific" {
			t.Fatalf("run %d: rewrite = %q, want specific", i, got)
		}
	}
}

func TestRewriteTextMapUsesLongestReplacementFirst(t *testing.T) {
	mapping := map[string]string{"building.md": "short", "./building.md": "specific"}
	for i := 0; i < 100; i++ {
		if got := rewriteTextMap("[Building](./building.md)", mapping); got != "[Building](specific)" {
			t.Fatalf("run %d: rewrite = %q", i, got)
		}
	}
}

func TestRewriteTextMapEqualLengthKeysAreStable(t *testing.T) {
	mapping := map[string]string{"ab": "x", "bc": "y"}
	for i := 0; i < 100; i++ {
		if got := rewriteTextMap("abc", mapping); got != "xc" {
			t.Fatalf("run %d: rewrite = %q", i, got)
		}
	}
}
