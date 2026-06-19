package schema

import (
	"strings"
	"testing"
)

func TestMermaid(t *testing.T) {
	s, err := FromSQL(`
		CREATE TABLE authors (id INTEGER PRIMARY KEY, name TEXT NOT NULL);
		CREATE TABLE books (
			id INTEGER PRIMARY KEY,
			title TEXT,
			author_id INTEGER REFERENCES authors(id)
		);
	`)
	if err != nil {
		t.Fatal(err)
	}

	out := s.Mermaid()

	if !strings.HasPrefix(out, "erDiagram") {
		t.Errorf("expected erDiagram header, got:\n%s", out)
	}
	for _, want := range []string{
		"authors {",
		"books {",
		"INTEGER id PK",
		"INTEGER author_id FK",
		`books }o--|| authors : "author_id"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("expected output to contain %q, got:\n%s", want, out)
		}
	}
}

func TestMermaidName(t *testing.T) {
	cases := map[string]string{
		"users":        "users",
		"order-items":  "order_items",
		"a.b":          "a_b",
		"":             "_",
		"VARCHAR(255)": "VARCHAR_255_",
	}
	for in, want := range cases {
		if got := mermaidName(in); got != want {
			t.Errorf("mermaidName(%q) = %q, want %q", in, got, want)
		}
	}
}
