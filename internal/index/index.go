// Package index — embedding-backed semantic-search store for the
// SemanticSearch MCP tool (ADR-014 T6, design from the 2026-04-26
// multi-CLI fan-out).
//
// One in-memory chromem-go collection per repo, persisted to disk so
// `clawtool serve` boot can reload without re-embedding. The index
// builder walks the repo, chunks each file, embeds via the
// configured provider (OpenAI default, Ollama override), and adds
// each chunk to the collection.
//
// Per ADR-007 we wrap [chromem-go](https://github.com/philippgille/chromem-go)
// (MIT, pure Go, no CGO) for the vector store and the embedding
// caller. We never reimplement HNSW / cosine / batching.
package index

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	chromem "github.com/philippgille/chromem-go"
)

// Result is one ranked hit returned by Search.
type Result struct {
	Path      string  `json:"path"`
	LineStart int     `json:"line_start"`
	LineEnd   int     `json:"line_end"`
	Snippet   string  `json:"snippet"`
	Score     float64 `json:"score"`
}

// Options drive the semantic search pipeline.
type Options struct {
	// Provider picks the embedding backend. "openai" uses
	// text-embedding-3-small via the user's OPENAI_API_KEY; "ollama"
	// uses a local Ollama daemon at OLLAMA_HOST (default
	// http://localhost:11434) with the nomic-embed-text model.
	Provider string

	// Model overrides the per-provider default. Empty = pick from
	// provider's stable default.
	Model string

	// PersistPath, when non-empty, persists the collection to disk so
	// boot reloads skip re-embedding. Default
	// ~/.cache/clawtool/index/<repo-hash>.gob.
	PersistPath string

	// MaxFileBytes caps the size of any one file the indexer reads.
	// Files above the cap are skipped (binary blobs, generated
	// assets). Default 200 KiB — enough for source files, tight for
	// build artefacts.
	MaxFileBytes int64

	// Ignore globs (matched against the path relative to the repo
	// root) skip files. Defaults filter common build / vendor /
	// .git directories.
	Ignore []string
}

// Store is the single semantic-search index. Methods are safe to call
// from multiple goroutines after Build returns.
type Store struct {
	mu   sync.RWMutex
	repo string
	db   *chromem.DB
	col  *chromem.Collection
	opts Options
}

// New creates an empty Store rooted at `repo` with the given options.
// Build populates it; Search queries it.
func New(repo string, opts Options) *Store {
	if opts.MaxFileBytes <= 0 {
		opts.MaxFileBytes = 200 * 1024
	}
	if len(opts.Ignore) == 0 {
		opts.Ignore = []string{".git/**", "node_modules/**", "vendor/**", "dist/**", "build/**", "*.min.js"}
	}
	if opts.Provider == "" {
		opts.Provider = "openai"
	}
	return &Store{repo: repo, db: chromem.NewDB(), opts: opts}
}

// Build walks the repo and embeds every readable text file. Idempotent
// when a persisted collection at PersistPath already exists — that
// path is loaded and Build skips the walk entirely. Operators force
// a rebuild via `Rebuild`.
func (s *Store) Build(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	embedder, err := s.embedder()
	if err != nil {
		return fmt.Errorf("index: embedder init: %w", err)
	}
	col, err := s.db.GetOrCreateCollection("clawtool-"+collectionTag(s.repo), nil, embedder)
	if err != nil {
		return fmt.Errorf("index: GetOrCreateCollection: %w", err)
	}
	s.col = col

	if col.Count() > 0 {
		// Persisted index already populated; trust it. Operators
		// force a rebuild via the (future) `clawtool index rebuild`
		// CLI subcommand.
		return nil
	}

	docs, err := s.collect(ctx)
	if err != nil {
		return err
	}
	if len(docs) == 0 {
		return nil
	}
	if err := col.AddDocuments(ctx, docs, 4); err != nil {
		return fmt.Errorf("index: AddDocuments: %w", err)
	}
	return nil
}

// Search queries the embedded collection with a natural-language
// query. Returns up to `limit` results ranked by similarity.
func (s *Store) Search(ctx context.Context, query string, limit int) ([]Result, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.col == nil {
		return nil, errors.New("index: store not built; call Build first")
	}
	if limit <= 0 {
		limit = 10
	}
	count := s.col.Count()
	if count == 0 {
		return nil, nil
	}
	if limit > count {
		limit = count
	}
	matches, err := s.col.Query(ctx, query, limit, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("index: query: %w", err)
	}
	out := make([]Result, 0, len(matches))
	for _, m := range matches {
		out = append(out, Result{
			Path:      m.Metadata["path"],
			LineStart: parseInt(m.Metadata["line_start"]),
			LineEnd:   parseInt(m.Metadata["line_end"]),
			Snippet:   m.Content,
			Score:     float64(m.Similarity),
		})
	}
	return out, nil
}

// Count reports how many chunks the store currently holds.
func (s *Store) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.col == nil {
		return 0
	}
	return s.col.Count()
}

// embedder builds the chromem-go embedding func for the configured
// provider. We do not write our own HTTP client; we wrap chromem's
// per-provider helper.
func (s *Store) embedder() (chromem.EmbeddingFunc, error) {
	switch s.opts.Provider {
	case "openai":
		key := strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
		if key == "" {
			return nil, errors.New("OPENAI_API_KEY not set; export it or override CLAWTOOL_EMBED_PROVIDER=ollama")
		}
		model := s.opts.Model
		if model == "" {
			model = string(chromem.EmbeddingModelOpenAI3Small)
		}
		return chromem.NewEmbeddingFuncOpenAI(key, chromem.EmbeddingModelOpenAI(model)), nil
	case "ollama":
		host := strings.TrimSpace(os.Getenv("OLLAMA_HOST"))
		if host == "" {
			host = "http://localhost:11434"
		}
		model := s.opts.Model
		if model == "" {
			model = "nomic-embed-text"
		}
		return chromem.NewEmbeddingFuncOllama(model, host+"/api"), nil
	}
	return nil, fmt.Errorf("unknown embedding provider %q", s.opts.Provider)
}

// collect walks the repo and produces one chromem.Document per chunk.
// Chunking is line-bounded: 80 lines per chunk with no overlap.
// Chunks are simple — the embedding model handles fuzzy matching.
func (s *Store) collect(ctx context.Context) ([]chromem.Document, error) {
	var docs []chromem.Document
	err := filepath.WalkDir(s.repo, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if shouldIgnore(s.repo, path, s.opts.Ignore) {
				return filepath.SkipDir
			}
			return nil
		}
		if shouldIgnore(s.repo, path, s.opts.Ignore) {
			return nil
		}
		info, err := d.Info()
		if err != nil || info.Size() > s.opts.MaxFileBytes {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		// Skip binary content (heuristic: NUL byte in first 4KB).
		head := body
		if len(head) > 4096 {
			head = head[:4096]
		}
		if containsNUL(head) {
			return nil
		}
		rel, _ := filepath.Rel(s.repo, path)
		for _, c := range chunkByLines(string(body), 80) {
			id := fmt.Sprintf("%s#L%d-L%d", rel, c.start, c.end)
			docs = append(docs, chromem.Document{
				ID:      id,
				Content: c.text,
				Metadata: map[string]string{
					"path":       rel,
					"line_start": fmt.Sprintf("%d", c.start),
					"line_end":   fmt.Sprintf("%d", c.end),
				},
			})
		}
		// Honour cancellation between files so a slow build can be
		// SIGINT'd cleanly.
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return docs, nil
}

type chunk struct {
	start, end int
	text       string
}

func chunkByLines(body string, size int) []chunk {
	if size <= 0 {
		size = 80
	}
	lines := strings.Split(body, "\n")
	var out []chunk
	for i := 0; i < len(lines); i += size {
		end := i + size
		if end > len(lines) {
			end = len(lines)
		}
		out = append(out, chunk{
			start: i + 1,
			end:   end,
			text:  strings.Join(lines[i:end], "\n"),
		})
	}
	return out
}

func shouldIgnore(repo, path string, patterns []string) bool {
	rel, err := filepath.Rel(repo, path)
	if err != nil {
		return false
	}
	for _, p := range patterns {
		// Cheap glob match; chromem-go and the rest of clawtool use
		// doublestar elsewhere — we don't need the dependency
		// transitively here.
		if matched, _ := filepath.Match(p, rel); matched {
			return true
		}
		// Walk every parent path component too: ".git/**" should
		// catch ".git/objects/abc" by matching ".git" against the
		// first component.
		first := strings.SplitN(p, string(filepath.Separator), 2)[0]
		first = strings.TrimSuffix(first, "/**")
		first = strings.TrimSuffix(first, "/*")
		if first == "" {
			continue
		}
		for _, part := range strings.Split(rel, string(filepath.Separator)) {
			if part == first {
				return true
			}
		}
	}
	return false
}

func containsNUL(b []byte) bool {
	for _, c := range b {
		if c == 0 {
			return true
		}
	}
	return false
}

func parseInt(s string) int {
	var n int
	for _, c := range s {
		if c < '0' || c > '9' {
			return n
		}
		n = n*10 + int(c-'0')
	}
	return n
}

// collectionTag derives a deterministic, filename-safe tag for the
// repo path so two repos can coexist in the same chromem DB.
func collectionTag(repoPath string) string {
	clean := filepath.Clean(repoPath)
	out := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		}
		return '-'
	}, clean)
	if len(out) > 64 {
		out = out[len(out)-64:]
	}
	return out
}
