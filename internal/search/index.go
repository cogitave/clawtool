// Package search powers clawtool's ToolSearch primitive.
//
// Per ADR-004 / ADR-005, search-first is clawtool's identity feature: when
// the catalog grows past a few dozen tools, agents can't reasonably hold
// every schema in context, so they need to *find* the right tool by
// natural-language query before binding to it. The MCP wire still exposes
// every tool; ToolSearch is the cheap discovery primitive that lets agents
// avoid materialising every schema upfront.
//
// Per ADR-007 we don't write a search engine — we wrap one. The chosen
// engine is bleve (github.com/blevesearch/bleve/v2): pure-Go, BM25 by
// default, supports phrase + fuzzy + boosted-field queries. We use the
// in-memory variant so there's no on-disk index to manage.
package search

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/mapping"
)

// Doc is one tool's searchable surface. Only the fields a query realistically
// matches on are indexed; we deliberately don't index the full input schema
// because long JSON Schema text dilutes the relevance signal.
type Doc struct {
	Name        string   // wire-form name, e.g. "Bash", "github-personal__create_issue"
	Description string   // human-readable; main signal
	Type        string   // "core" or "sourced"
	Instance    string   // empty for core; instance name for sourced
	Keywords    []string // optional extra search terms (synonyms, aliases)
}

// Hit is one ranked search result.
type Hit struct {
	Name        string  `json:"name"`
	Score       float64 `json:"score"`
	Description string  `json:"description"`
	Type        string  `json:"type"`
	Instance    string  `json:"instance,omitempty"`
}

// Index is a built search index over a fixed set of tool descriptors.
//
// Lifetime: built once at clawtool serve startup from the union of (enabled
// core tools, aggregated source tools, ToolSearch itself). Concurrent
// reads are safe; bleve handles internal locking. The index is not
// rebuilt on the fly — adding sources at runtime needs a server restart
// (acknowledged limitation; v0.6 hot-reload addresses it).
type Index struct {
	bleve  bleve.Index
	docs   map[string]Doc // id → Doc, for hit hydration
	totals struct {
		all     int
		core    int
		sourced int
	}
}

// Build constructs an in-memory bleve index from the given docs. Returns an
// error only when bleve mapping or insert fails — both indicate a clawtool
// bug, not user input.
func Build(docs []Doc) (*Index, error) {
	im, err := bleve.NewMemOnly(buildMapping())
	if err != nil {
		return nil, fmt.Errorf("bleve mem-only: %w", err)
	}
	idx := &Index{
		bleve: im,
		docs:  make(map[string]Doc, len(docs)),
	}
	for _, d := range docs {
		if d.Name == "" {
			return nil, errors.New("Doc with empty Name")
		}
		searchable := struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			Keywords    string `json:"keywords"`
			Type        string `json:"type"`
			Instance    string `json:"instance"`
		}{
			Name:        d.Name,
			Description: d.Description,
			Keywords:    strings.Join(d.Keywords, " "),
			Type:        d.Type,
			Instance:    d.Instance,
		}
		if err := im.Index(d.Name, searchable); err != nil {
			return nil, fmt.Errorf("index %q: %w", d.Name, err)
		}
		idx.docs[d.Name] = d
		idx.totals.all++
		switch d.Type {
		case "core":
			idx.totals.core++
		case "sourced":
			idx.totals.sourced++
		}
	}
	return idx, nil
}

// Total returns the indexed-document count.
func (i *Index) Total() int { return i.totals.all }

// Search runs a natural-language query and returns the top `limit` hits.
//
// Query semantics: bleve's QueryStringQuery — which accepts free-form text
// plus optional field boosts. We apply a name^3 boost so "Bash" matches
// the literal tool more strongly than tools that mention bash in their
// descriptions. typeFilter "core" / "sourced" restricts results; "" or
// "any" returns everything.
func (i *Index) Search(query string, limit int, typeFilter string) ([]Hit, error) {
	if i == nil {
		return nil, errors.New("nil index")
	}
	if strings.TrimSpace(query) == "" {
		return nil, errors.New("empty query")
	}
	if limit <= 0 {
		limit = 8
	}
	if limit > 50 {
		limit = 50
	}

	q := bleve.NewQueryStringQuery(boostQuery(query))
	req := bleve.NewSearchRequestOptions(q, limit, 0, false)
	req.Fields = []string{"name", "description", "type", "instance"}

	res, err := i.bleve.Search(req)
	if err != nil {
		return nil, fmt.Errorf("bleve search: %w", err)
	}

	out := make([]Hit, 0, len(res.Hits))
	for _, h := range res.Hits {
		d, ok := i.docs[h.ID]
		if !ok {
			// Defensive: a stored doc somehow vanished from our map.
			continue
		}
		if typeFilter != "" && typeFilter != "any" && d.Type != typeFilter {
			continue
		}
		out = append(out, Hit{
			Name:        d.Name,
			Score:       h.Score,
			Description: d.Description,
			Type:        d.Type,
			Instance:    d.Instance,
		})
	}
	// Sort by score descending; bleve's default order is already this but
	// we re-sort defensively after the type filter for stability.
	sort.SliceStable(out, func(a, b int) bool { return out[a].Score > out[b].Score })
	return out, nil
}

// boostQuery rewrites the user's query to give the `name` field 3× weight
// over the default. This is a cheap heuristic that empirically improves
// results when the user types a tool name fragment ("bash" should rank
// the literal Bash tool above any other tool that just mentions bash).
func boostQuery(q string) string {
	q = strings.TrimSpace(q)
	if q == "" {
		return q
	}
	// Already field-targeted? Don't double-rewrite.
	if strings.Contains(q, ":") {
		return q
	}
	return fmt.Sprintf("name:(%s)^3 OR description:(%s) OR keywords:(%s)^2", q, q, q)
}

// buildMapping defines how bleve analyses each document field.
//
// Only the textual signal fields (name, description, keywords) get
// full-text analysis; type and instance are kept as keyword (single-token)
// for exact-match filters.
func buildMapping() mapping.IndexMapping {
	im := bleve.NewIndexMapping()
	doc := bleve.NewDocumentMapping()

	textField := bleve.NewTextFieldMapping()
	textField.Analyzer = "standard"
	doc.AddFieldMappingsAt("name", textField)
	doc.AddFieldMappingsAt("description", textField)
	doc.AddFieldMappingsAt("keywords", textField)

	keywordField := bleve.NewTextFieldMapping()
	keywordField.Analyzer = "keyword"
	doc.AddFieldMappingsAt("type", keywordField)
	doc.AddFieldMappingsAt("instance", keywordField)

	im.DefaultMapping = doc
	return im
}
