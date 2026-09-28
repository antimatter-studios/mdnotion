package main

import (
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/dstotijn/go-notion"
)

type orphanRoundTrip func(*http.Request) (*http.Response, error)

func (fn orphanRoundTrip) RoundTrip(req *http.Request) (*http.Response, error) { return fn(req) }

func orphanFixture(t *testing.T, children map[string][]map[string]any, deleted *[]string) *NotionClient {
	t.Helper()
	client := notion.NewClient("test-token", notion.WithHTTPClient(&http.Client{
		Transport: orphanRoundTrip(func(req *http.Request) (*http.Response, error) {
			id := strings.TrimPrefix(req.URL.Path, "/v1/blocks/")
			id = strings.TrimSuffix(id, "/children")
			var payload any
			switch req.Method {
			case http.MethodGet:
				if _, ok := children[id]; !ok {
					t.Errorf("unexpected page %s", id)
				}
				payload = map[string]any{"results": children[id], "has_more": false}
			case http.MethodDelete:
				*deleted = append(*deleted, id)
				payload = map[string]any{"id": id, "type": "child_page", "child_page": map[string]any{"title": id}}
			default:
				t.Errorf("unexpected request: %s %s", req.Method, req.URL)
				return &http.Response{StatusCode: http.StatusBadRequest, Body: io.NopCloser(strings.NewReader(`{}`)), Header: make(http.Header)}, nil
			}
			body, err := json.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(string(body))), Header: make(http.Header)}, nil
		}),
	}))
	return &NotionClient{NotionClient: client}
}

func child(id string) map[string]any {
	return map[string]any{"id": id, "type": "child_page", "child_page": map[string]any{"title": id}}
}

func metadata() map[string]any {
	return map[string]any{"id": "meta", "type": "code", "code": map[string]any{
		"language": "json", "rich_text": []any{map[string]any{"plain_text": `{"mdnotion":{"content_hash":"old"}}`}},
	}}
}

func TestFindOrphansReportsOnlyManagedPagesAndPrunesTopmost(t *testing.T) {
	children := map[string][]map[string]any{
		"root":      {child("current"), child("old"), child("manual")},
		"current":   {metadata()},
		"old":       {metadata(), child("old-child")},
		"old-child": {metadata()},
		"manual":    {child("handmade")},
		"handmade":  {metadata()},
	}
	var deleted []string
	client := orphanFixture(t, children, &deleted)
	orphans, err := findOrphanPages(client, []node{{id: "root", kind: kindPage}}, []*planNode{{pageID: "current"}})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, orphan := range orphans {
		ids = append(ids, orphan.id)
	}
	if !reflect.DeepEqual(ids, []string{"handmade", "old", "old-child"}) {
		t.Fatalf("orphans %v", ids)
	}
	if len(deleted) != 0 {
		t.Fatal("reporting must not delete")
	}
	if err := pruneOrphanPages(client, orphans); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(deleted, []string{"handmade", "old"}) {
		t.Fatalf("deleted %v, want topmost orphan subtrees", deleted)
	}
}

func TestPruneRefusesOrphanContainingPlannedPage(t *testing.T) {
	children := map[string][]map[string]any{
		"root":       {child("old-parent")},
		"old-parent": {metadata(), child("planned")},
		"planned":    {metadata()},
	}
	var deleted []string
	client := orphanFixture(t, children, &deleted)
	orphans, err := findOrphanPages(client, []node{{id: "root", kind: kindPage}}, []*planNode{{pageID: "planned"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(orphans) != 1 || orphans[0].id != "old-parent" {
		t.Fatalf("orphans: %+v", orphans)
	}
	if err := pruneOrphanPages(client, orphans); err == nil || !strings.Contains(err.Error(), "contains a planned page") {
		t.Fatalf("expected protected-descendant error, got %v", err)
	}
	if len(deleted) != 0 {
		t.Fatalf("deleted protected page: %v", deleted)
	}
}
