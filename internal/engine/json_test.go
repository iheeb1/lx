package engine

import (
	"fmt"
	"strings"
	"testing"
)

func issuesJSON(n int) string {
	var b strings.Builder
	b.WriteString("[\n")
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteString(",\n")
		}
		fmt.Fprintf(&b, `  {
    "url": "https://api.example.com/issues/%d",
    "html_url": "https://example.com/issues/%d",
    "number": %d,
    "title": "Issue number %d with a fairly long descriptive title that goes on and on beyond eighty",
    "state": "open",
    "user": {"login": "user%d", "id": %d, "avatar_url": "https://example.com/a/%d", "type": "User", "site_admin": false},
    "labels": [{"name": "bug"}],
    "body": "line one\nline %d"
  }`, i, i, i, i, i, i, i, i)
	}
	b.WriteString("\n]")
	return b.String()
}

func TestCompactJSONTable(t *testing.T) {
	in := issuesJSON(35)
	out, ok := CompactJSON(in, 100)
	if !ok {
		t.Fatal("not compacted")
	}
	lines := strings.Split(out, "\n")
	if lines[0] != "[table: 35 items]" || lines[1] != "keys: number, title, user, body" ||
		lines[2] != "all: state=open, labels=[bug]" {
		t.Fatalf("header:\n%s", strings.Join(lines[:3], "\n"))
	}
	row := `0 | Issue number 0 with a fairly long descriptive title that goes on and on beyond … | {"login":"user0","id":0,"type":"User","site_admin":false} | line one\nline 0`
	if lines[3] != row {
		t.Errorf("row:\n%s\nwant:\n%s", lines[3], row)
	}
	if !strings.Contains(out, "… +15 more items") || !strings.HasSuffix(out, " *_url fields]") || strings.Contains(out, "https://") {
		t.Errorf("truncation/url note missing:\n%s", out)
	}
	if out2, _ := CompactJSON(in, 100); out2 != out {
		t.Error("not deterministic")
	}
}

func TestCompactJSONErrorFieldsFirst(t *testing.T) {
	in := `{
  "data": null,
  "items": [1, 2, 3],
  "meta": {"request_id": "abc", "error": "upstream timeout"},
  "message": "Validation Failed",
  "errors": [{"field": "title", "code": "missing"}]
}`
	out, ok := CompactJSON(in, 0)
	if !ok {
		t.Fatal("not compacted")
	}
	lines := strings.Split(out, "\n")
	want := []string{`meta.error: "upstream timeout"`, `message: "Validation Failed"`, `errors: [{"field":"title","code":"missing"}]`}
	for i, w := range want {
		if lines[i] != w {
			t.Errorf("line %d = %q, want %q\n%s", i, lines[i], w, out)
		}
	}
}

func TestCompactJSONRejects(t *testing.T) {
	for _, in := range []string{"", "   ", "not json", `{"a": 1`, `{"a":1} trailing`, "[1,2,3]"} {
		if out, ok := CompactJSON(in, 0); ok {
			t.Errorf("CompactJSON(%q) = %q, ok", in, out)
		}
	}

	if _, ok := CompactJSON(issuesJSON(3), 1_000_000); ok {
		t.Error("compacted within budget")
	}
}

func TestCompactJSONNDJSONAndUnicode(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 5; i++ {
		fmt.Fprintf(&b, "{\"name\": \"naïve-%d ✓\", \"size\": %d, \"tags\": [\"a\", \"b\"]}\r\n", i, i*10)
	}
	out, ok := CompactJSON(b.String(), 0)
	if !ok {
		t.Fatal("NDJSON not compacted")
	}
	if !strings.Contains(out, "naïve-4 ✓ | 40") {
		t.Errorf("unicode row missing:\n%s", out)
	}
}
