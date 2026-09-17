package main

import (
	"reflect"
	"testing"
)

func TestSplitTitlePath(t *testing.T) {
	cases := []struct {
		name string
		path string
		want []string
	}{
		{"single segment", "Docs", []string{"Docs"}},
		{"plain path", "Engineering/Docs/InPace", []string{"Engineering", "Docs", "InPace"}},
		{"escaped slash inside title", `Engineering/CI\/CD Pipeline/Docs`, []string{"Engineering", "CI/CD Pipeline", "Docs"}},
		{"escaped slash at start of segment", `\/etc/Docs`, []string{"/etc", "Docs"}},
		{"lone backslash kept verbatim", `Win\Path/Docs`, []string{`Win\Path`, "Docs"}},
		{"trailing backslash kept verbatim", `Docs\`, []string{`Docs\`}},
		{"empty segment preserved for validation", "Docs//Foo", []string{"Docs", "", "Foo"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := splitTitlePath(tc.path)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("splitTitlePath(%q) = %#v, want %#v", tc.path, got, tc.want)
			}
		})
	}
}

func TestValidateTitlePathRejectsEmptySegments(t *testing.T) {
	for _, path := range []string{"", "Docs//Foo", "/Docs", "Docs/", "Docs/ /Foo"} {
		if _, err := validateTitlePath(path); err == nil {
			t.Errorf("validateTitlePath(%q) expected error, got nil", path)
		}
	}
	segments, err := validateTitlePath(`A/B\/C`)
	if err != nil {
		t.Fatalf("validateTitlePath returned unexpected error: %v", err)
	}
	if !reflect.DeepEqual(segments, []string{"A", "B/C"}) {
		t.Errorf("validateTitlePath segments = %#v", segments)
	}
}

func TestLooksLikePageID(t *testing.T) {
	cases := []struct {
		value string
		want  bool
	}{
		{"25301aea292980c089d7e48bbed6365a", true},
		{"25301aea-2929-80c0-89d7-e48bbed6365a", true},
		{"Engineering/Docs", false},
		{"v1.2", false},
		{"25301aea292980c089d7e48bbed6365", false},  // 31 chars
		{"z5301aea292980c089d7e48bbed6365a", false}, // non-hex
	}
	for _, tc := range cases {
		if got := looksLikePageID(tc.value); got != tc.want {
			t.Errorf("looksLikePageID(%q) = %t, want %t", tc.value, got, tc.want)
		}
	}
}
