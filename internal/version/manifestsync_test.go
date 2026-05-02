// Unit tests for the manifest-rewrite helpers. The codegen-vs-live
// invariant lives in release_pipeline_test.go (uses these same
// helpers against the real .claude-plugin/*.json files).
package version

import (
	"strings"
	"testing"
)

func TestSyncPluginJSON_RewritesVersionAndPreservesEverythingElse(t *testing.T) {
	in := []byte(`{
  "name": "clawtool",
  "version": "0.21.7",
  "description": "test fixture",
  "mcpServers": {
    "tools": {
      "command": "clawtool"
    }
  }
}
`)
	out, err := SyncPluginJSON(in, "0.22.119")
	if err != nil {
		t.Fatalf("SyncPluginJSON: %v", err)
	}
	if !strings.Contains(string(out), `"version": "0.22.119"`) {
		t.Errorf("output missing rewritten version: %s", out)
	}
	if strings.Contains(string(out), `"version": "0.21.7"`) {
		t.Errorf("output still carries old version: %s", out)
	}
	// Field-order + structural fields must be byte-preserved
	// outside the version line.
	for _, want := range []string{
		`"name": "clawtool"`,
		`"description": "test fixture"`,
		`"mcpServers": {`,
		`"tools": {`,
		`"command": "clawtool"`,
	} {
		if !strings.Contains(string(out), want) {
			t.Errorf("output dropped %q:\n%s", want, out)
		}
	}
	if out[len(out)-1] != '\n' {
		t.Errorf("trailing newline dropped")
	}
}

func TestSyncPluginJSON_RejectsMultipleVersionFields(t *testing.T) {
	in := []byte(`{
  "version": "0.1.0",
  "nested": { "version": "0.2.0" }
}
`)
	if _, err := SyncPluginJSON(in, "9.9.9"); err == nil {
		t.Error("expected error on >1 version field, got nil")
	}
}

func TestSyncPluginJSON_RejectsZeroVersionFields(t *testing.T) {
	in := []byte(`{ "name": "clawtool" }`)
	if _, err := SyncPluginJSON(in, "9.9.9"); err == nil {
		t.Error("expected error on 0 version fields, got nil")
	}
}

func TestSyncMarketplaceJSON_RewritesBothVersions(t *testing.T) {
	in := []byte(`{
  "name": "clawtool-marketplace",
  "metadata": {
    "description": "test",
    "version": "0.21.7"
  },
  "plugins": [
    {
      "name": "clawtool",
      "version": "0.21.7",
      "license": "MIT"
    }
  ]
}
`)
	out, err := SyncMarketplaceJSON(in, "0.22.119")
	if err != nil {
		t.Fatalf("SyncMarketplaceJSON: %v", err)
	}
	hits := strings.Count(string(out), `"version": "0.22.119"`)
	if hits != 2 {
		t.Errorf("expected 2 rewritten version fields, got %d:\n%s", hits, out)
	}
	if strings.Contains(string(out), `"version": "0.21.7"`) {
		t.Errorf("output still carries old version: %s", out)
	}
	for _, want := range []string{
		`"name": "clawtool-marketplace"`,
		`"plugins": [`,
		`"license": "MIT"`,
	} {
		if !strings.Contains(string(out), want) {
			t.Errorf("output dropped %q:\n%s", want, out)
		}
	}
}

func TestSyncMarketplaceJSON_RejectsWrongCount(t *testing.T) {
	in := []byte(`{ "metadata": { "version": "0.1.0" } }`)
	if _, err := SyncMarketplaceJSON(in, "9.9.9"); err == nil {
		t.Error("expected error on 1 version field, got nil")
	}
}

func TestSyncIsIdempotent(t *testing.T) {
	in := []byte(`{
  "version": "0.22.119"
}
`)
	out1, err := SyncPluginJSON(in, "0.22.119")
	if err != nil {
		t.Fatalf("first sync: %v", err)
	}
	out2, err := SyncPluginJSON(out1, "0.22.119")
	if err != nil {
		t.Fatalf("second sync: %v", err)
	}
	if string(out1) != string(out2) {
		t.Errorf("not idempotent:\nfirst:  %s\nsecond: %s", out1, out2)
	}
}
