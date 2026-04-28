package cli

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestBuildSetupMatrix_IncludesDaemonAndIdentity confirms the matrix
// always offers the foundational items regardless of host / recipe
// state. Without these the operator can opt into bridges/claims but
// nothing actually works.
func TestBuildSetupMatrix_IncludesDaemonAndIdentity(t *testing.T) {
	a := New()
	items := buildSetupMatrix(a, t.TempDir())
	keys := map[string]bool{}
	for _, it := range items {
		keys[it.key] = true
	}
	for _, expected := range []string{"daemon", "identity", "secrets"} {
		if !keys[expected] {
			t.Errorf("matrix missing foundational item %q", expected)
		}
	}
}

// TestBuildSetupMatrix_ItemKeysUnique catches the obvious refactor
// hazard — two items collapsing to the same MultiSelect key would
// silently drop one from the operator's choices.
func TestBuildSetupMatrix_ItemKeysUnique(t *testing.T) {
	a := New()
	items := buildSetupMatrix(a, t.TempDir())
	seen := map[string]bool{}
	for _, it := range items {
		if seen[it.key] {
			t.Errorf("duplicate matrix key %q", it.key)
		}
		seen[it.key] = true
	}
}

// TestBuildSetupMatrix_ApplyHonoursWiring confirms the apply
// callbacks dispatch through the package-level vars instead of
// no-op'ing. We swap one var, run the matrix item, and assert the
// stub fired. Catches a regression where a new helper forgets to
// register itself in init().
func TestBuildSetupMatrix_ApplyHonoursWiring(t *testing.T) {
	a := New()
	items := buildSetupMatrix(a, t.TempDir())
	var daemonItem matrixItem
	for _, it := range items {
		if it.key == "daemon" {
			daemonItem = it
			break
		}
	}
	if daemonItem.key == "" {
		t.Fatal("daemon item missing")
	}

	prev := runDaemonEnsure
	defer func() { runDaemonEnsure = prev }()
	called := false
	runDaemonEnsure = func(ctx context.Context) error {
		called = true
		return errors.New("stub-call ok")
	}

	err := daemonItem.apply(a, context.Background(), "")
	if !called {
		t.Error("daemon apply didn't dispatch through runDaemonEnsure")
	}
	if err == nil || !strings.Contains(err.Error(), "stub-call ok") {
		t.Errorf("expected stub error, got %v", err)
	}
}
