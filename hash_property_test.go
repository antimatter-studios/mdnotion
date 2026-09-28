package main

import (
	"strings"
	"testing"

	"github.com/dstotijn/go-notion"
)

func TestContentHashPropertyRequiresRichTextColumn(t *testing.T) {
	props := notion.DatabasePageProperties{}
	_, err := contentHashProperty(props, "Content Hash", "Projects (database-id)")
	if err == nil || !strings.Contains(err.Error(), "Projects (database-id)") || !strings.Contains(err.Error(), "Content Hash") {
		t.Fatalf("missing property error = %v", err)
	}
	props["Content Hash"] = notion.DatabasePageProperty{Type: "number"}
	_, err = contentHashProperty(props, "Content Hash", "Projects (database-id)")
	if err == nil || !strings.Contains(err.Error(), "rich_text") {
		t.Fatalf("wrong-type property error = %v", err)
	}
}

func TestContentHashPropertyAllowsEmptyOrStoredHash(t *testing.T) {
	props := notion.DatabasePageProperties{
		"Content Hash": {Type: "rich_text"},
	}
	if got, err := contentHashProperty(props, "Content Hash", "Projects"); err != nil || got != "" {
		t.Fatalf("empty hash = %q, %v", got, err)
	}
	props["Content Hash"] = notion.DatabasePageProperty{
		Type:     "rich_text",
		RichText: []notion.RichText{{PlainText: "abc123"}},
	}
	if got, err := contentHashProperty(props, "Content Hash", "Projects"); err != nil || got != "abc123" {
		t.Fatalf("stored hash = %q, %v", got, err)
	}
}

func TestDatabaseHashPropertyPreflight(t *testing.T) {
	db := notion.Database{
		ID:    "database-id",
		Title: []notion.RichText{{PlainText: "Projects"}},
		Properties: notion.DatabaseProperties{
			"Name": {Type: "title"},
		},
	}
	if err := databaseHashProperty(db, "Content Hash"); err == nil ||
		!strings.Contains(err.Error(), "Projects") || !strings.Contains(err.Error(), "Content Hash") {
		t.Fatalf("missing schema property error = %v", err)
	}
	db.Properties["Content Hash"] = notion.DatabaseProperty{Type: "rich_text"}
	if err := databaseHashProperty(db, "Content Hash"); err != nil {
		t.Fatalf("valid schema rejected: %v", err)
	}
}
