package sources

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestBenchRegression_DropExceedsThreshold writes a TSV with a 50%
// hit rate plus a baseline at 100% (delta = 50pp) and confirms one
// Idea fires.
func TestBenchRegression_DropExceedsThreshold(t *testing.T) {
	dir := t.TempDir()
	tsv := filepath.Join(dir, "bench.tsv")
	tsvBody := "# header\n" +
		"q1\tToolA\tToolA\t1.0\n" +
		"q2\tToolB\tToolB\t1.0\n" +
		"q3\tToolC\tToolX\t0.5\n" +
		"q4\tToolD\tToolY\t0.5\n"
	if err := os.WriteFile(tsv, []byte(tsvBody), 0o644); err != nil {
		t.Fatalf("write tsv: %v", err)
	}
	baselinePath := filepath.Join(dir, "baseline.json")
	bl := Baseline{HitRate: 1.0, NumQueries: 4, WrittenAt: "2026-04-01T00:00:00Z"}
	body, _ := json.MarshalIndent(bl, "", "  ")
	if err := os.WriteFile(baselinePath, body, 0o644); err != nil {
		t.Fatalf("write baseline: %v", err)
	}

	src := &BenchRegression{TSVPath: tsv, BaselinePath: baselinePath}
	ideas, err := src.Scan(context.Background(), dir)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(ideas) != 1 {
		t.Fatalf("Scan returned %d ideas, want 1", len(ideas))
	}
	if !strings.Contains(ideas[0].Title, "BM25 regression") {
		t.Fatalf("Title: %q", ideas[0].Title)
	}
	if ideas[0].SuggestedPriority < 5 {
		t.Fatalf("priority too low: %d", ideas[0].SuggestedPriority)
	}
}

// TestBenchRegression_NoTSV is a no-op (cheap-on-fail).
func TestBenchRegression_NoTSV(t *testing.T) {
	src := &BenchRegression{TSVPath: filepath.Join(t.TempDir(), "missing.tsv")}
	ideas, err := src.Scan(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(ideas) != 0 {
		t.Fatalf("Scan returned %d ideas, want 0", len(ideas))
	}
}

// TestBenchRegression_NoBaseline is a no-op when only the TSV
// exists (operator hasn't ratified a floor yet).
func TestBenchRegression_NoBaseline(t *testing.T) {
	dir := t.TempDir()
	tsv := filepath.Join(dir, "bench.tsv")
	if err := os.WriteFile(tsv, []byte("q1\tToolA\tToolA\t1.0\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	src := &BenchRegression{TSVPath: tsv, BaselinePath: filepath.Join(dir, "missing.json")}
	ideas, err := src.Scan(context.Background(), dir)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(ideas) != 0 {
		t.Fatalf("Scan returned %d ideas, want 0", len(ideas))
	}
}

// TestBenchRegression_DropBelowThresholdNoOp confirms drops below
// Threshold don't surface as ideas. Test gives a 1-row TSV with a
// hit (1.0 hit rate) so the drop vs a 1.0 baseline is exactly 0 —
// well under the 5pp default — and asserts no Idea fires.
func TestBenchRegression_DropBelowThresholdNoOp(t *testing.T) {
	dir := t.TempDir()
	tsv := filepath.Join(dir, "bench.tsv")
	if err := os.WriteFile(tsv, []byte("q\tA\tA\t1.0\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	baselinePath := filepath.Join(dir, "baseline.json")
	bl := Baseline{HitRate: 1.0, NumQueries: 1, WrittenAt: "now"}
	body, _ := json.MarshalIndent(bl, "", "  ")
	if err := os.WriteFile(baselinePath, body, 0o644); err != nil {
		t.Fatalf("write baseline: %v", err)
	}
	src := &BenchRegression{TSVPath: tsv, BaselinePath: baselinePath, Threshold: 0.05}
	ideas, err := src.Scan(context.Background(), dir)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(ideas) != 0 {
		t.Fatalf("Scan returned %d ideas, want 0", len(ideas))
	}
}

// TestSaveBaseline_RoundTrip writes a TSV, calls SaveBaseline, then
// reads back the JSON to confirm shape.
func TestSaveBaseline_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	tsv := filepath.Join(dir, "bench.tsv")
	if err := os.WriteFile(tsv, []byte("q1\tA\tA\t1.0\nq2\tB\tC\t0.5\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	baselinePath := filepath.Join(dir, "baseline.json")
	bl, err := SaveBaseline(tsv, baselinePath)
	if err != nil {
		t.Fatalf("SaveBaseline: %v", err)
	}
	if bl.NumQueries != 2 || bl.HitRate != 0.5 {
		t.Fatalf("Baseline: %+v", bl)
	}
	body, err := os.ReadFile(baselinePath)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(body), "\"hit_rate\"") {
		t.Fatalf("baseline body missing fields: %s", body)
	}
}
