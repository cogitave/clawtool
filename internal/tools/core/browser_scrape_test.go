package core

import (
	"context"
	"strings"
	"testing"
)

func TestBrowserScrape_MissingBinary(t *testing.T) {
	prev := obscuraBin
	obscuraBin = func() string { return "" }
	defer func() { obscuraBin = prev }()

	res := executeBrowserScrape(context.Background(), browserScrapeArgs{
		URLs:        []string{"https://a.example", "https://b.example"},
		Eval:        "document.title",
		Concurrency: 2,
		WaitUntil:   "load",
		TimeoutMs:   5000,
	})
	if !strings.Contains(res.ErrorReason, "obscura") {
		t.Errorf("expected install hint, got %q", res.ErrorReason)
	}
}

func TestBrowserScrape_EmptyURLs(t *testing.T) {
	prev := obscuraBin
	obscuraBin = func() string { return "/usr/bin/true" }
	defer func() { obscuraBin = prev }()

	res := executeBrowserScrape(context.Background(), browserScrapeArgs{
		Eval:      "document.title",
		WaitUntil: "load",
		TimeoutMs: 5000,
	})
	if !strings.Contains(res.ErrorReason, "urls list") {
		t.Errorf("expected empty-urls error, got %q", res.ErrorReason)
	}
}

func TestBrowserScrape_ParseArrayJSON(t *testing.T) {
	rows := parseScrapeJSON([]byte(`[{"url":"https://a","result":"Hello"},{"url":"https://b","error":"timeout"}]`))
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
	if rows[0].Result != "Hello" || rows[0].URL != "https://a" {
		t.Errorf("row 0 wrong: %+v", rows[0])
	}
	if rows[1].Error != "timeout" {
		t.Errorf("row 1 error not surfaced: %+v", rows[1])
	}
}

func TestBrowserScrape_ParseNDJSON(t *testing.T) {
	body := `{"url":"https://a","result":"one"}
{"url":"https://b","value":"two"}`
	rows := parseScrapeJSON([]byte(body))
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
	if rows[0].Result != "one" {
		t.Errorf("row 0 result wrong: %+v", rows[0])
	}
	if rows[1].Result != "two" {
		t.Errorf("row 1 fallback to value field failed: %+v", rows[1])
	}
}

func TestSplitURLs_Mixed(t *testing.T) {
	in := "https://a.test\nhttps://b.test, https://c.test\nftp://nope, , https://d.test"
	got := splitURLs(in)
	want := []string{"https://a.test", "https://b.test", "https://c.test", "https://d.test"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d] %q != %q", i, got[i], want[i])
		}
	}
}
