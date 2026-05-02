// Package sources — bench_regression source.
//
// Reads /tmp/clawtool-toolsearch-bench.tsv (the toolsearch BM25
// rank-1 hit-rate fixture the autodev cron writes after each
// manifest tweak) and compares the latest run against a stored
// baseline at $XDG_CONFIG_HOME/clawtool/ideator/bench-baseline.json.
// A drop greater than DefaultRegressionThreshold (5pp) emits one
// Idea so the operator decides whether to chase the regression or
// roll the baseline forward.
//
// Missing TSV → silent no-op (CI hasn't written one yet).
// Missing baseline → silent no-op (operator hasn't run
// `clawtool ideate --baseline-set` yet).
package sources

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/cogitave/clawtool/internal/atomicfile"
	"github.com/cogitave/clawtool/internal/ideator"
	"github.com/cogitave/clawtool/internal/xdg"
)

// DefaultRegressionThreshold is the rank-1 hit-rate drop that
// promotes a TSV row into an Idea. 5pp is loud enough that the
// operator wants to know, quiet enough that single-test variance
// (one query per ideate cycle) doesn't trip it constantly.
const DefaultRegressionThreshold = 0.05

// DefaultBenchTSV is the canonical TSV path the autodev cron
// writes. Columns: query, expected_top1, observed_top1, score.
const DefaultBenchTSV = "/tmp/clawtool-toolsearch-bench.tsv"

// BenchRegression implements IdeaSource for the BM25 baseline diff.
type BenchRegression struct {
	// TSVPath overrides DefaultBenchTSV (tests inject tmpdir).
	TSVPath string
	// BaselinePath overrides the default baseline location.
	BaselinePath string
	// Threshold is the hit-rate drop (0..1) at which an Idea fires.
	Threshold float64
}

// NewBenchRegression returns a ready-to-use BM25 baseline-diff
// source with default paths and threshold.
func NewBenchRegression() *BenchRegression {
	return &BenchRegression{
		TSVPath:      DefaultBenchTSV,
		BaselinePath: DefaultBenchBaselinePath(),
		Threshold:    DefaultRegressionThreshold,
	}
}

// DefaultBenchBaselinePath returns
// $XDG_CONFIG_HOME/clawtool/ideator/bench-baseline.json.
func DefaultBenchBaselinePath() string {
	return filepath.Join(xdg.ConfigDir(), "ideator", "bench-baseline.json")
}

// Name returns the canonical source name.
func (BenchRegression) Name() string { return "bench_regression" }

// Baseline is the on-disk JSON shape the operator writes via
// `clawtool ideate --baseline-set`. HitRate is rank-1 (top-1
// correct count / total queries).
type Baseline struct {
	HitRate    float64 `json:"hit_rate"`
	NumQueries int     `json:"num_queries"`
	WrittenAt  string  `json:"written_at"`
}

// Scan loads the latest TSV results, computes the rank-1 hit rate,
// compares against the baseline, and emits one Idea when the drop
// exceeds Threshold.
func (b BenchRegression) Scan(ctx context.Context, repoRoot string) ([]ideator.Idea, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	rows, err := readBenchTSV(b.tsvPath())
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	current := computeHitRate(rows)

	baseline, err := loadBaseline(b.baselinePath())
	if err != nil {
		return nil, err
	}
	if baseline == nil {
		return nil, nil
	}
	threshold := b.Threshold
	if threshold <= 0 {
		threshold = DefaultRegressionThreshold
	}
	drop := baseline.HitRate - current
	if drop < threshold {
		return nil, nil
	}
	hash := sha1.Sum([]byte(fmt.Sprintf("bench:%v->%v", baseline.HitRate, current)))
	return []ideator.Idea{{
		Title:             fmt.Sprintf("BM25 regression: %.1fpp drop", drop*100),
		Summary:           fmt.Sprintf("ToolSearch BM25 rank-1 hit rate dropped from %.2f → %.2f over %d queries (-%.1fpp).", baseline.HitRate, current, len(rows), drop*100),
		Evidence:          fmt.Sprintf("%s vs %s", b.tsvPath(), b.baselinePath()),
		SuggestedPriority: 8,
		SuggestedPrompt: fmt.Sprintf(
			"Investigate the ToolSearch BM25 rank-1 hit-rate regression.\n\n"+
				"  - baseline: %.2f (n=%d)\n  - current:  %.2f (n=%d)\n  - drop:     %.1fpp (threshold %.1fpp)\n\n"+
				"Diff the latest tool descriptions / keywords against the previous green\n"+
				"version, identify which manifest entry caused the rank slip, and either\n"+
				"land a fix that restores the baseline OR run\n"+
				"`clawtool ideate --baseline-set` to ratify the new floor (only after the\n"+
				"operator confirms it's intentional).",
			baseline.HitRate, baseline.NumQueries,
			current, len(rows),
			drop*100, threshold*100),
		DedupeKey: "bench_regression:" + hex.EncodeToString(hash[:]),
	}}, nil
}

// SaveBaseline writes the current TSV's hit rate to the baseline
// path. CLI's `--baseline-set` calls this. Exposed at package
// level so the orchestrator + CLI share one writer.
func SaveBaseline(tsvPath, baselinePath string) (Baseline, error) {
	if tsvPath == "" {
		tsvPath = DefaultBenchTSV
	}
	if baselinePath == "" {
		baselinePath = DefaultBenchBaselinePath()
	}
	rows, err := readBenchTSV(tsvPath)
	if err != nil {
		return Baseline{}, err
	}
	if len(rows) == 0 {
		return Baseline{}, fmt.Errorf("bench_regression: no rows in %s", tsvPath)
	}
	bl := Baseline{
		HitRate:    computeHitRate(rows),
		NumQueries: len(rows),
		WrittenAt:  nowRFC3339(),
	}
	body, err := json.MarshalIndent(bl, "", "  ")
	if err != nil {
		return Baseline{}, err
	}
	if err := atomicfile.WriteFileMkdir(baselinePath, body, 0o644, 0o755); err != nil {
		return Baseline{}, err
	}
	return bl, nil
}

func (b BenchRegression) tsvPath() string {
	if b.TSVPath != "" {
		return b.TSVPath
	}
	return DefaultBenchTSV
}

func (b BenchRegression) baselinePath() string {
	if b.BaselinePath != "" {
		return b.BaselinePath
	}
	return DefaultBenchBaselinePath()
}

// benchRow is one TSV line: query \t expected \t observed \t score.
type benchRow struct {
	Query    string
	Expected string
	Observed string
	Score    float64
}

// readBenchTSV parses the TSV. Missing file → empty slice + nil
// error (cheap-on-fail).
func readBenchTSV(path string) ([]benchRow, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("bench_regression: read %s: %w", path, err)
	}
	var rows []benchRow
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 3 {
			continue
		}
		row := benchRow{
			Query:    fields[0],
			Expected: fields[1],
			Observed: fields[2],
		}
		if len(fields) >= 4 {
			row.Score, _ = strconv.ParseFloat(fields[3], 64)
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// computeHitRate returns rank-1 accuracy: fraction of rows where
// observed == expected.
func computeHitRate(rows []benchRow) float64 {
	if len(rows) == 0 {
		return 0
	}
	hits := 0
	for _, r := range rows {
		if r.Observed == r.Expected {
			hits++
		}
	}
	return float64(hits) / float64(len(rows))
}

// loadBaseline reads the JSON baseline. Missing file → nil + nil
// error (cheap-on-fail).
func loadBaseline(path string) (*Baseline, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("bench_regression: read baseline %s: %w", path, err)
	}
	var bl Baseline
	if err := json.Unmarshal(body, &bl); err != nil {
		return nil, fmt.Errorf("bench_regression: parse baseline: %w", err)
	}
	return &bl, nil
}

// nowRFC3339 is a tiny indirection so tests could stub time. The
// timestamp goes into the JSON baseline for human audit; the
// regression decision doesn't depend on it.
var nowRFC3339 = func() string {
	return time.Now().UTC().Format(time.RFC3339)
}
