package search

import (
	"strings"
	"testing"
)

func sample() []Doc {
	return []Doc{
		{Name: "Bash", Description: "Run a shell command via /bin/bash with timeout-safe execution.", Type: "core"},
		{Name: "Grep", Description: "Search file contents for a regular-expression pattern, powered by ripgrep.", Type: "core"},
		{Name: "Read", Description: "Read a file with stable line cursors. Format-aware: text, PDF, ipynb.", Type: "core"},
		{Name: "ToolSearch", Description: "Find tools by natural-language query. Returns ranked candidates.", Type: "core"},
		{Name: "github__create_issue", Description: "Create a new issue in a GitHub repository.", Type: "sourced", Instance: "github"},
		{Name: "github__list_pulls", Description: "List pull requests for a GitHub repository.", Type: "sourced", Instance: "github"},
		{Name: "stub__echo", Description: "Echo input back, prefixed with echo:.", Type: "sourced", Instance: "stub"},
	}
}

func mustBuild(t *testing.T, docs []Doc) *Index {
	t.Helper()
	idx, err := Build(docs)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return idx
}

func TestBuild_Total(t *testing.T) {
	idx := mustBuild(t, sample())
	if got := idx.Total(); got != 7 {
		t.Errorf("Total = %d, want 7", got)
	}
}

func TestBuild_RejectsEmptyName(t *testing.T) {
	_, err := Build([]Doc{{Name: "", Description: "x"}})
	if err == nil {
		t.Error("expected error for empty Name")
	}
}

func TestSearch_RanksLiteralNameHigh(t *testing.T) {
	idx := mustBuild(t, sample())
	hits, err := idx.Search("bash", 5, "")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("no hits")
	}
	if hits[0].Name != "Bash" {
		t.Errorf("top hit = %q, want Bash (literal name boost)", hits[0].Name)
	}
}

func TestSearch_FindsBySemanticDescription(t *testing.T) {
	idx := mustBuild(t, sample())
	// "search file contents" should rank Grep highly even though "Grep"
	// itself wasn't typed.
	hits, err := idx.Search("search file contents regular expression", 5, "")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("no hits")
	}
	if hits[0].Name != "Grep" {
		t.Errorf("top hit for 'search file contents regex' = %q, want Grep; full hits = %v", hits[0].Name, hits)
	}
}

func TestSearch_FindsSourcedTool(t *testing.T) {
	idx := mustBuild(t, sample())
	hits, err := idx.Search("create issue github", 5, "")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("no hits")
	}
	if hits[0].Name != "github__create_issue" {
		t.Errorf("top hit = %q, want github__create_issue; full hits = %+v", hits[0].Name, hits)
	}
	if hits[0].Type != "sourced" || hits[0].Instance != "github" {
		t.Errorf("hit metadata wrong: type=%q instance=%q", hits[0].Type, hits[0].Instance)
	}
}

func TestSearch_TypeFilter(t *testing.T) {
	idx := mustBuild(t, sample())
	hits, err := idx.Search("create echo issue", 10, "core")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	for _, h := range hits {
		if h.Type != "core" {
			t.Errorf("typeFilter=core but got %q (type=%q)", h.Name, h.Type)
		}
	}

	hits2, err := idx.Search("anything", 10, "sourced")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	for _, h := range hits2 {
		if h.Type != "sourced" {
			t.Errorf("typeFilter=sourced but got %q (type=%q)", h.Name, h.Type)
		}
	}
}

func TestSearch_LimitCap(t *testing.T) {
	idx := mustBuild(t, sample())
	hits, err := idx.Search("a", 100, "")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) > 50 {
		t.Errorf("hits = %d, want <= 50 (hard cap)", len(hits))
	}
}

func TestSearch_RejectsEmptyQuery(t *testing.T) {
	idx := mustBuild(t, sample())
	if _, err := idx.Search("   ", 5, ""); err == nil {
		t.Error("expected error for whitespace-only query")
	}
}

func TestSearch_KeywordsBoost(t *testing.T) {
	docs := sample()
	docs = append(docs, Doc{
		Name:        "MysteryTool",
		Description: "Does something useful.",
		Type:        "core",
		Keywords:    []string{"frob", "frobnicate"},
	})
	idx := mustBuild(t, docs)
	hits, err := idx.Search("frobnicate", 5, "")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("no hits for keyword-only query")
	}
	if hits[0].Name != "MysteryTool" {
		t.Errorf("keyword search top hit = %q, want MysteryTool", hits[0].Name)
	}
}

func TestSearch_ScoresMonotonicallyDescending(t *testing.T) {
	idx := mustBuild(t, sample())
	hits, _ := idx.Search("file content read pdf", 10, "")
	for i := 1; i < len(hits); i++ {
		if hits[i-1].Score < hits[i].Score {
			t.Errorf("hits not descending: %d->%d (%v->%v)", i-1, i, hits[i-1], hits[i])
		}
	}
}

// Sanity: ensure bleve actually understands what we feed it (compile + run smoke).
func TestSearch_SmokeReturnsKnownDescription(t *testing.T) {
	idx := mustBuild(t, sample())
	hits, _ := idx.Search("ripgrep", 5, "")
	if len(hits) == 0 || hits[0].Name != "Grep" {
		t.Errorf("'ripgrep' should top-rank Grep; got %+v", hits)
	}
	if !strings.Contains(hits[0].Description, "ripgrep") {
		t.Errorf("description should be hydrated from doc, got %q", hits[0].Description)
	}
}
