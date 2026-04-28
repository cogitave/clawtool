package registry

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/server"
)

// TestExportTypeScript_RoundTrips writes the manifest to a tmp dir
// and verifies (a) one .ts per spec, (b) an index.ts barrel, (c)
// the per-tool file carries the description verbatim, (d) the
// barrel re-exports every name.
func TestExportTypeScript_RoundTrips(t *testing.T) {
	m := New()
	m.Append(ToolSpec{
		Name:        "Foo",
		Description: "Does the foo thing. Has a long enough description to wrap.",
		Keywords:    []string{"foo", "thing"},
		Category:    CategoryShell,
		Gate:        "Foo",
		Register:    func(*server.MCPServer, Runtime) {},
	})
	m.Append(ToolSpec{
		Name:        "Bar",
		Description: "Bar tool — short.",
		Category:    CategoryFile,
	})

	dir := t.TempDir()
	written, err := m.ExportTypeScript(dir)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	want := []string{"Bar.ts", "Foo.ts", "index.ts"}
	if len(written) != len(want) {
		t.Fatalf("written = %v, want %v", written, want)
	}
	for i, w := range want {
		if written[i] != w {
			t.Errorf("written[%d] = %q, want %q", i, written[i], w)
		}
	}

	fooBody, err := os.ReadFile(filepath.Join(dir, "Foo.ts"))
	if err != nil {
		t.Fatalf("read Foo.ts: %v", err)
	}
	foo := string(fooBody)
	if !strings.Contains(foo, "Does the foo thing.") {
		t.Errorf("Foo.ts missing description; got:\n%s", foo)
	}
	if !strings.Contains(foo, "export declare function Foo(input: any): Promise<any>;") {
		t.Errorf("Foo.ts missing function signature; got:\n%s", foo)
	}
	if !strings.Contains(foo, "@keywords foo, thing") {
		t.Errorf("Foo.ts missing keywords tag; got:\n%s", foo)
	}
	if !strings.Contains(foo, "Category: shell") {
		t.Errorf("Foo.ts missing category header; got:\n%s", foo)
	}
	if !strings.Contains(foo, "Config gate: Foo") {
		t.Errorf("Foo.ts missing gate header; got:\n%s", foo)
	}

	indexBody, err := os.ReadFile(filepath.Join(dir, "index.ts"))
	if err != nil {
		t.Fatalf("read index.ts: %v", err)
	}
	idx := string(indexBody)
	if !strings.Contains(idx, `export { Foo } from "./Foo";`) {
		t.Errorf("index.ts missing Foo re-export; got:\n%s", idx)
	}
	if !strings.Contains(idx, `export { Bar } from "./Bar";`) {
		t.Errorf("index.ts missing Bar re-export; got:\n%s", idx)
	}
}

// TestExportTypeScript_EmptyManifest fails fast — generating a
// stubs dir for nothing is almost certainly a config bug.
func TestExportTypeScript_EmptyManifest(t *testing.T) {
	m := New()
	_, err := m.ExportTypeScript(t.TempDir())
	if err == nil {
		t.Fatal("expected error on empty manifest, got nil")
	}
}
