package core

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestRead_CSV_HeaderAndRows(t *testing.T) {
	dir := t.TempDir()
	body := "name,city,score\nalpha,Istanbul,42\nbravo,Berlin,17\ncharlie,Tokyo,99\n"
	path := writeFile(t, dir, "data.csv", body)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res := executeRead(ctx, path, 1, 0, "")

	if res.Format != "csv" {
		t.Errorf("format = %q, want csv", res.Format)
	}
	if res.Engine != "csv-stdlib" {
		t.Errorf("engine = %q, want csv-stdlib", res.Engine)
	}
	if !strings.Contains(res.Content, "# columns (3): name | city | score") {
		t.Errorf("missing header line in content:\n%s", res.Content)
	}
	if !strings.Contains(res.Content, "alpha | Istanbul | 42") {
		t.Errorf("missing first row:\n%s", res.Content)
	}
	if !strings.Contains(res.Content, "# total data rows: 3") {
		t.Errorf("missing total-rows footer:\n%s", res.Content)
	}
}

func TestRead_TSV_TabDelimiter(t *testing.T) {
	dir := t.TempDir()
	body := "k\tv\nfoo\t1\nbar\t2\n"
	path := writeFile(t, dir, "data.tsv", body)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res := executeRead(ctx, path, 1, 0, "")

	if res.Format != "tsv" {
		t.Errorf("format = %q, want tsv", res.Format)
	}
	if !strings.Contains(res.Content, "# columns (2): k | v") {
		t.Errorf("tsv header missing: %q", res.Content)
	}
	if !strings.Contains(res.Content, "foo | 1") {
		t.Errorf("tsv row missing: %q", res.Content)
	}
}

func TestRead_StructuredFormatsTaggedButPassthrough(t *testing.T) {
	cases := []struct {
		name, ext, body string
		wantFormat      string
	}{
		{"json", "data.json", `{"a":1,"b":[2,3]}`, "json"},
		{"yaml", "data.yaml", "a: 1\nb:\n  - 2\n  - 3\n", "yaml"},
		{"toml", "data.toml", "a = 1\n[section]\nb = \"x\"\n", "toml"},
		{"xml", "data.xml", "<root><child>x</child></root>", "xml"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			path := writeFile(t, dir, c.ext, c.body)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			res := executeRead(ctx, path, 1, 0, "")
			if res.Format != c.wantFormat {
				t.Errorf("format = %q, want %q", res.Format, c.wantFormat)
			}
			if res.Engine != "stdlib" {
				t.Errorf("engine = %q, want stdlib (passthrough)", res.Engine)
			}
			if !strings.Contains(res.Content, strings.TrimRight(c.body, "\n")) {
				t.Errorf("content lost characters:\n  got=%q\n want substring %q", res.Content, c.body)
			}
		})
	}
}
