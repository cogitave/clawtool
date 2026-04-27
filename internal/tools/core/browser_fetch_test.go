package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeObscuraScript writes a fake `obscura` shim that prints `out` on
// stdout, exits exitCode. Returns the bin path to point obscuraBin at.
func fakeObscuraScript(t *testing.T, out string, exitCode int) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "obscura")
	body := "#!/bin/sh\ncat <<'__EOF__'\n" + out + "\n__EOF__\nexit " + itoa(exitCode) + "\n"
	if err := os.WriteFile(bin, []byte(body), 0o755); err != nil {
		t.Fatalf("write fake obscura: %v", err)
	}
	return bin
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [12]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func TestBrowserFetch_MissingBinary(t *testing.T) {
	prev := obscuraBin
	obscuraBin = func() string { return "" }
	defer func() { obscuraBin = prev }()

	res := executeBrowserFetch(context.Background(), browserFetchArgs{
		URL:       "https://example.com",
		WaitUntil: "load",
		TimeoutMs: 5000,
	})
	if res.ErrorReason == "" {
		t.Fatal("expected install hint when obscura is missing")
	}
	if !strings.Contains(res.ErrorReason, "obscura") {
		t.Errorf("error should name obscura: %q", res.ErrorReason)
	}
}

func TestBrowserFetch_BadURL(t *testing.T) {
	prev := obscuraBin
	obscuraBin = func() string { return "/nonexistent" } // never invoked because URL is bad first
	defer func() { obscuraBin = prev }()

	res := executeBrowserFetch(context.Background(), browserFetchArgs{
		URL:       "ftp://example.com",
		WaitUntil: "load",
		TimeoutMs: 5000,
	})
	if !strings.Contains(res.ErrorReason, "http://") {
		t.Errorf("expected http(s) scheme error: %q", res.ErrorReason)
	}
}

func TestBrowserFetch_HTML_RendersReadable(t *testing.T) {
	html := "<html><head><title>Hi</title></head><body><article><h1>Hi</h1><p>Body of the article that the readability extractor will pick up because it has enough textual signal to count as the main content region rather than chrome around it.</p></article></body></html>"
	bin := fakeObscuraScript(t, html, 0)
	prev := obscuraBin
	obscuraBin = func() string { return bin }
	defer func() { obscuraBin = prev }()

	res := executeBrowserFetch(context.Background(), browserFetchArgs{
		URL:       "https://example.com",
		WaitUntil: "load",
		TimeoutMs: 10000,
	})
	if res.ErrorReason != "" {
		t.Fatalf("unexpected error: %s", res.ErrorReason)
	}
	if res.Format != "html" {
		t.Errorf("Format = %q, want html", res.Format)
	}
	if !strings.Contains(res.Content, "Hi") {
		t.Errorf("Content missing title: %q", res.Content)
	}
	if res.SizeBytes == 0 {
		t.Error("SizeBytes should reflect the rendered body")
	}
}

func TestBrowserFetch_Eval_PassesValueThrough(t *testing.T) {
	bin := fakeObscuraScript(t, "Hello from eval", 0)
	prev := obscuraBin
	obscuraBin = func() string { return bin }
	defer func() { obscuraBin = prev }()

	res := executeBrowserFetch(context.Background(), browserFetchArgs{
		URL:       "https://example.com",
		WaitUntil: "load",
		Eval:      "document.title",
		TimeoutMs: 10000,
	})
	if res.ErrorReason != "" {
		t.Fatalf("unexpected error: %s", res.ErrorReason)
	}
	if res.Format != "eval" {
		t.Errorf("Format = %q, want eval", res.Format)
	}
	if !strings.Contains(res.EvalResult, "Hello from eval") {
		t.Errorf("EvalResult missing payload: %q", res.EvalResult)
	}
}

func TestBrowserFetch_NonZero_SurfacesError(t *testing.T) {
	bin := fakeObscuraScript(t, "boom", 2)
	prev := obscuraBin
	obscuraBin = func() string { return bin }
	defer func() { obscuraBin = prev }()

	res := executeBrowserFetch(context.Background(), browserFetchArgs{
		URL:       "https://example.com",
		WaitUntil: "load",
		TimeoutMs: 10000,
	})
	if res.ErrorReason == "" {
		t.Fatal("expected an error from non-zero exit")
	}
	if !strings.Contains(res.ErrorReason, "obscura") {
		t.Errorf("error should mention obscura: %q", res.ErrorReason)
	}
}
