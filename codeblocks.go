package main

import (
	"strings"

	"github.com/brittonhayes/notionmd/chunk"
	"github.com/dstotijn/go-notion"
	"github.com/gomarkdown/markdown/ast"
	"github.com/gomarkdown/markdown/parser"
)

// Notion's code.language enum: https://developers.notion.com/reference/block#code
var notionCodeLanguages = map[string]bool{
	"abap": true, "arduino": true, "bash": true, "basic": true, "c": true,
	"clojure": true, "coffeescript": true, "c++": true, "c#": true,
	"css": true, "dart": true, "diff": true, "docker": true, "elixir": true,
	"elm": true, "erlang": true, "flow": true, "fortran": true, "f#": true,
	"gherkin": true, "glsl": true, "go": true, "graphql": true, "groovy": true,
	"haskell": true, "html": true, "java": true, "javascript": true,
	"json": true, "julia": true, "kotlin": true, "latex": true, "less": true,
	"lisp": true, "livescript": true, "lua": true, "makefile": true,
	"markdown": true, "markup": true, "matlab": true, "mermaid": true,
	"nix": true, "objective-c": true, "ocaml": true, "pascal": true,
	"perl": true, "php": true, "plain text": true, "powershell": true,
	"prolog": true, "protobuf": true, "python": true, "r": true,
	"reason": true, "ruby": true, "rust": true, "sass": true,
	"scala": true, "scheme": true, "scss": true, "shell": true,
	"sql": true, "swift": true, "typescript": true, "vb.net": true,
	"verilog": true, "vhdl": true, "visual basic": true,
	"webassembly": true, "xml": true, "yaml": true, "java/c/c++/c#": true,
}

var codeLanguageAliases = map[string]string{
	"sh": "shell", "zsh": "shell", "console": "shell", "text": "plain text",
	"txt": "plain text", "js": "javascript", "jsx": "javascript",
	"ts": "typescript", "tsx": "typescript", "py": "python",
	"rb": "ruby", "cs": "c#", "cpp": "c++", "yml": "yaml",
	"golang": "go", "dockerfile": "docker",
}

func mapLanguageToNotionCompatible(language string) string {
	language = strings.ToLower(strings.TrimSpace(language))
	if alias, ok := codeLanguageAliases[language]; ok {
		language = alias
	}
	if notionCodeLanguages[language] {
		return language
	}
	return "plain text"
}

func languageFromFence(node *ast.CodeBlock) string {
	fields := strings.Fields(string(node.Info))
	if len(fields) == 0 {
		return "plain text"
	}
	return mapLanguageToNotionCompatible(fields[0])
}

func codeFromAST(node *ast.CodeBlock) notion.CodeBlock {
	language := languageFromFence(node)
	return notion.CodeBlock{RichText: chunk.RichText(string(node.Literal), nil), Language: &language}
}

// notionmd validates fence info before returning blocks, so aliases and
// unsupported names disappear. Parse with the same Markdown extensions and
// restore normalized languages onto its code blocks before publication.
// Its list converter also drops code children; add those at their AST position.
func recoverCodeBlocks(content []byte, blocks []notion.Block) []notion.Block {
	document := parser.NewWithExtensions(parser.CommonExtensions).Parse(content)
	next := 0
	for _, node := range document.GetChildren() {
		switch node := node.(type) {
		case *ast.CodeBlock:
			if len(node.Literal) == 0 {
				continue
			}
			for next < len(blocks) {
				language := languageFromFence(node)
				if code, ok := blocks[next].(*notion.CodeBlock); ok {
					copy := *code
					copy.Language = &language
					blocks[next] = copy
					next++
					break
				}
				if code, ok := blocks[next].(notion.CodeBlock); ok {
					code.Language = &language
					blocks[next] = code
					next++
					break
				}
				next++
			}
		case *ast.List:
			for _, item := range node.GetChildren() {
				for next < len(blocks) && !isListBlock(blocks[next]) {
					next++
				}
				if next == len(blocks) {
					break
				}
				blocks[next] = recoverListItem(item, blocks[next])
				next++
			}
		}
	}
	return blocks
}

func isListBlock(block notion.Block) bool {
	switch block.(type) {
	case notion.BulletedListItemBlock, notion.NumberedListItemBlock:
		return true
	}
	return false
}

func recoverListItem(item ast.Node, block notion.Block) notion.Block {
	children := item.GetChildren()
	if len(children) == 0 {
		return block
	}
	var original []notion.Block
	switch b := block.(type) {
	case notion.BulletedListItemBlock:
		original = b.Children
	case notion.NumberedListItemBlock:
		original = b.Children
	default:
		return block
	}
	patched := make([]notion.Block, 0, len(original))
	used := 0
	for _, child := range children[1:] {
		switch child := child.(type) {
		case *ast.CodeBlock:
			if len(child.Literal) > 0 {
				patched = append(patched, codeFromAST(child))
			}
		case *ast.List:
			for _, nested := range child.GetChildren() {
				if used < len(original) {
					patched = append(patched, recoverListItem(nested, original[used]))
					used++
				}
			}
		}
	}
	patched = append(patched, original[used:]...)
	switch b := block.(type) {
	case notion.BulletedListItemBlock:
		b.Children = patched
		return b
	case notion.NumberedListItemBlock:
		b.Children = patched
		return b
	}
	return block
}
