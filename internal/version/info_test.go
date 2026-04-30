package version

import (
	"encoding/json"
	"runtime"
	"strings"
	"testing"
)

// TestInfo_HasRequiredFields confirms Info() populates the
// fields every external probe (telemetry, /v1/health, monitoring
// scripts) depends on. Drives the contract for `clawtool version
// --json` output.
func TestInfo_HasRequiredFields(t *testing.T) {
	bi := Info()
	if bi.Name == "" {
		t.Error("Name is empty")
	}
	if bi.Version == "" {
		t.Error("Version is empty")
	}
	if bi.GoVersion == "" {
		t.Error("GoVersion is empty")
	}
	if bi.Platform == "" {
		t.Error("Platform is empty")
	}
	if !strings.Contains(bi.Platform, "/") {
		t.Errorf("Platform = %q, want GOOS/GOARCH form", bi.Platform)
	}
	if !strings.HasPrefix(bi.GoVersion, "go") {
		t.Errorf("GoVersion = %q, want a go-prefixed runtime.Version() string", bi.GoVersion)
	}
	// Sanity: Platform must reflect the actual build target, not
	// a stale string. Catches accidental hard-coding.
	want := runtime.GOOS + "/" + runtime.GOARCH
	if bi.Platform != want {
		t.Errorf("Platform = %q, want %q", bi.Platform, want)
	}
}

// TestInfoJSON_ParseableSnakeCase confirms InfoJSON() emits
// parseable JSON whose top-level keys are snake_case (matches
// the project-wide JSON convention shared with agents.Status
// and agentListEntry from earlier ticks).
func TestInfoJSON_ParseableSnakeCase(t *testing.T) {
	body, err := InfoJSON()
	if err != nil {
		t.Fatalf("InfoJSON: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v\nbody: %s", err, body)
	}
	for _, key := range []string{"name", "version", "go_version", "platform"} {
		if _, ok := got[key]; !ok {
			t.Errorf("missing required key %q in JSON output: %s", key, body)
		}
	}
	// JSON tags must produce snake_case literals in the rendered
	// output (catches accidental tag drift between code and the
	// documented contract).
	for _, lit := range []string{`"name":`, `"version":`, `"go_version":`, `"platform":`} {
		if !strings.Contains(body, lit) {
			t.Errorf("output missing literal %s; body: %s", lit, body)
		}
	}
}
