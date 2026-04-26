package core

import (
	"net/url"
	"os"
	"strings"

	readability "github.com/go-shiori/go-readability"
)

// readHTML uses go-readability (a Go port of Mozilla's Readability.js,
// the same algorithm Firefox Reader View uses) to strip nav / ads /
// boilerplate and return clean article text. ADR-007: we wrap, we don't
// rebuild; readability is the de-facto extractor for AI-agent pipelines.
//
// Output content is the extracted plain-text body, prefixed with a small
// metadata header so the agent can see what readability decided was the
// title and byline.
func readHTML(path string, lineStart, lineEnd int, res *ReadResult) {
	f, err := os.Open(path)
	if err != nil {
		res.ErrorReason = err.Error()
		return
	}
	defer f.Close()

	// Readability needs a base URL to resolve relative links; for a local
	// file we pass file:// which keeps internal links intact in case the
	// caller wants to follow them later.
	base, _ := url.Parse("file://" + path)
	article, err := readability.FromReader(f, base)
	if err != nil {
		res.ErrorReason = "readability: " + err.Error()
		return
	}

	var sb strings.Builder
	if article.Title != "" {
		sb.WriteString("# ")
		sb.WriteString(article.Title)
		sb.WriteByte('\n')
	}
	if article.Byline != "" {
		sb.WriteString("by ")
		sb.WriteString(article.Byline)
		sb.WriteByte('\n')
	}
	if article.SiteName != "" {
		sb.WriteString("site: ")
		sb.WriteString(article.SiteName)
		sb.WriteByte('\n')
	}
	if article.Excerpt != "" {
		sb.WriteString("\n> ")
		sb.WriteString(article.Excerpt)
		sb.WriteByte('\n')
	}
	sb.WriteString("\n")
	sb.WriteString(article.TextContent)
	sb.WriteByte('\n')

	applyLineRangeFromBuffer(sb.String(), lineStart, lineEnd, res)
}
