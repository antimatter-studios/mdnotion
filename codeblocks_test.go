package main

import (
	"testing"

	"github.com/brittonhayes/notionmd"
	"github.com/dstotijn/go-notion"
)

func TestFenceLanguagesSurviveConversion(t *testing.T) {
	content := "```golang\nfmt.Println(1)\n```\n\n```jsx\nconst x = 1\n```\n\n```toml\nx = 1\n```\n\n```bash\necho ok\n```\n"
	blocks, err := notionmd.Convert(content)
	if err != nil {
		t.Fatal(err)
	}
	blocks = validateContentBlocks(recoverCodeBlocks([]byte(content), blocks))
	want := []string{"go", "javascript", "plain text", "bash"}
	if len(blocks) != len(want) {
		t.Fatalf("got %d blocks, want %d", len(blocks), len(want))
	}
	for i, block := range blocks {
		code, ok := block.(notion.CodeBlock)
		if !ok || code.Language == nil || *code.Language != want[i] {
			t.Errorf("block %d language = %#v, want %q", i, block, want[i])
		}
	}
}

func TestNestedListCodeIsRecovered(t *testing.T) {
	content := "- setup\n\n    ```dockerfile\n    FROM alpine\n    ```\n\n    - nested\n\n        ```console\n        echo ok\n        ```\n"
	blocks, err := notionmd.Convert(content)
	if err != nil {
		t.Fatal(err)
	}
	blocks = validateContentBlocks(recoverCodeBlocks([]byte(content), blocks))
	if len(blocks) != 1 {
		t.Fatalf("got %d top-level blocks, want 1: %#v", len(blocks), blocks)
	}
	outer := blocks[0].(notion.BulletedListItemBlock)
	if len(outer.Children) != 2 {
		t.Fatalf("outer children = %#v", outer.Children)
	}
	if code := outer.Children[0].(notion.CodeBlock); code.Language == nil || *code.Language != "docker" {
		t.Errorf("outer code language = %#v, want docker", code.Language)
	}
	inner := outer.Children[1].(notion.BulletedListItemBlock)
	if len(inner.Children) != 1 {
		t.Fatalf("inner children = %#v", inner.Children)
	}
	if code := inner.Children[0].(notion.CodeBlock); code.Language == nil || *code.Language != "shell" {
		t.Errorf("inner code language = %#v, want shell", code.Language)
	}
}

func TestValidationVisitsOtherChildBlocks(t *testing.T) {
	unknown := "toml"
	blocks := validateContentBlocks([]notion.Block{
		&notion.QuoteBlock{Children: []notion.Block{
			&notion.ToggleBlock{Children: []notion.Block{
				&notion.CodeBlock{Language: &unknown},
			}},
		}},
	})
	quote := blocks[0].(notion.QuoteBlock)
	toggle := quote.Children[0].(notion.ToggleBlock)
	code := toggle.Children[0].(notion.CodeBlock)
	if code.Language == nil || *code.Language != "plain text" {
		t.Errorf("nested code language = %#v, want plain text", code.Language)
	}
}
