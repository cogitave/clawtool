package commits

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cogitave/clawtool/internal/setup"
)

func TestLefthook_Registered(t *testing.T) {
	r := setup.Lookup("lefthook")
	if r == nil {
		t.Fatal("lefthook should self-register")
	}
	if r.Meta().Category != setup.CategoryCommits {
		t.Errorf("wrong category: %q", r.Meta().Category)
	}
}

func TestLefthook_DetectAbsent(t *testing.T) {
	r := setup.Lookup("lefthook")
	dir := t.TempDir()
	status, _, _ := r.Detect(context.Background(), dir)
	if status != setup.StatusAbsent {
		t.Errorf("got %q, want Absent", status)
	}
}

func TestLefthook_ApplyDropsBothFiles(t *testing.T) {
	r := setup.Lookup("lefthook")
	dir := t.TempDir()
	if err := r.Apply(context.Background(), dir, nil); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	for _, rel := range []string{"lefthook.yml", ".commitlintrc.json"} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			t.Errorf("missing %s after Apply: %v", rel, err)
		}
	}
	body, _ := os.ReadFile(filepath.Join(dir, "lefthook.yml"))
	if !setup.HasMarker(body, setup.ManagedByMarker) {
		t.Error("lefthook.yml lacks marker")
	}
	if !strings.Contains(string(body), "commit-msg:") {
		t.Errorf("lefthook.yml missing commit-msg block: %s", body)
	}
	if err := r.Verify(context.Background(), dir); err != nil {
		t.Errorf("Verify: %v", err)
	}
}

func TestLefthook_RefusesPreexistingCommitlint(t *testing.T) {
	r := setup.Lookup("lefthook")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".commitlintrc.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := r.Apply(context.Background(), dir, nil); err == nil {
		t.Fatal("Apply should refuse pre-existing .commitlintrc.json")
	}
	if err := r.Apply(context.Background(), dir, setup.Options{"force": true}); err != nil {
		t.Errorf("Apply with force should succeed: %v", err)
	}
}

func TestLefthook_ApplyIsIdempotent(t *testing.T) {
	r := setup.Lookup("lefthook")
	dir := t.TempDir()
	if err := r.Apply(context.Background(), dir, nil); err != nil {
		t.Fatal(err)
	}
	// Re-Apply: lefthook.yml is marker-managed (idempotent) but
	// .commitlintrc.json refuses on a second run unless --force.
	// That's intentional — JSON can't carry a marker.
	if err := r.Apply(context.Background(), dir, setup.Options{"force": true}); err != nil {
		t.Errorf("re-Apply with --force should succeed; got %v", err)
	}
}

func TestLefthook_PrereqsCoverBothBinaries(t *testing.T) {
	r := setup.Lookup("lefthook")
	prs := r.Prereqs()
	if len(prs) != 2 {
		t.Fatalf("expected 2 prereqs (lefthook + npx); got %d", len(prs))
	}
	gotLefthook, gotNpx := false, false
	for _, p := range prs {
		if strings.Contains(strings.ToLower(p.Name), "lefthook") {
			gotLefthook = true
		}
		if strings.Contains(strings.ToLower(p.Name), "npx") {
			gotNpx = true
		}
	}
	if !gotLefthook || !gotNpx {
		t.Errorf("prereq names didn't cover lefthook + npx: %+v", prs)
	}
}
