package main

import (
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/dstotijn/go-notion"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return fn(req) }

func TestClearPageContentPreservesManifestChildren(t *testing.T) {
	var deleted []string
	client := notion.NewClient("test-token", notion.WithHTTPClient(&http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			var body string
			switch {
			case req.Method == http.MethodGet && req.URL.Query().Get("start_cursor") == "":
				body = `{"results":[
					{"id":"intro","type":"paragraph","paragraph":{"rich_text":[]}},
					{"id":"managed-guide","type":"child_page","child_page":{"title":"Guide"}}
				],"has_more":true,"next_cursor":"next"}`
			case req.Method == http.MethodGet && req.URL.Query().Get("start_cursor") == "next":
				body = `{"results":[
					{"id":"manual-db","type":"child_database","child_database":{"title":"Data"}},
					{"id":"body","type":"paragraph","paragraph":{"rich_text":[]}}
				],"has_more":false,"next_cursor":null}`
			case req.Method == http.MethodDelete:
				deleted = append(deleted, strings.TrimPrefix(req.URL.Path, "/v1/blocks/"))
				body = `{"id":"deleted","type":"paragraph","paragraph":{"rich_text":[]}}`
			default:
				t.Errorf("unexpected request: %s %s", req.Method, req.URL)
				return &http.Response{StatusCode: http.StatusBadRequest, Body: io.NopCloser(strings.NewReader(`{}`)), Header: make(http.Header)}, nil
			}
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
		}),
	}))
	if err := (&NotionClient{NotionClient: client}).ClearPageContent("parent"); err != nil {
		t.Fatalf("ClearPageContent: %v", err)
	}
	if !reflect.DeepEqual(deleted, []string{"intro", "body"}) {
		t.Fatalf("deleted %v, want only content blocks", deleted)
	}
}
