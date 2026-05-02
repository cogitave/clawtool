package checkpoint

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// initWipRepo builds a minimal git repo with one initial commit so
// Autocommit / Resolve have a base to operate against. Skips when
// git isn't on PATH.
func initWipRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	runs := [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.email", "t@t.t"},
		{"config", "user.name", "clawtool-test"},
		{"commit", "--allow-empty", "-m", "feat: init"},
	}
	for _, args := range runs {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, out)
		}
	}
	return dir
}

func writeFile(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func headSubject(t *testing.T, dir string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "log", "-1", "--format=%s").CombinedOutput()
	if err != nil {
		t.Fatalf("git log: %v (%s)", err, out)
	}
	return strings.TrimSpace(string(out))
}

func commitCount(t *testing.T, dir, base string) int {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "rev-list", "--count", base+"..HEAD").CombinedOutput()
	if err != nil {
		t.Fatalf("git rev-list: %v (%s)", err, out)
	}
	n := 0
	for _, c := range strings.TrimSpace(string(out)) {
		n = n*10 + int(c-'0')
	}
	return n
}

func TestPrependWipPrefix(t *testing.T) {
	cases := map[string]string{
		"draft change":           "wip!: draft change",
		"wip!: already prefixed": "wip!: already prefixed",
		"feat: real subject":     "wip!: feat: real subject",   // not auto-stripped
		"wip!:no-space-after":    "wip!:no-space-after",        // still considered prefixed
		"WIP!: case-different":   "wip!: WIP!: case-different", // case-sensitive
	}
	for in, want := range cases {
		if got := PrependWipPrefix(in); got != want {
			t.Errorf("PrependWipPrefix(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHasWipPrefix(t *testing.T) {
	cases := map[string]bool{
		"wip!: draft":           true,
		"wip!:no-space":         true,
		"feat: real":            false,
		"":                      false,
		"  wip!: leading-space": false, // anchored at first byte
	}
	for in, want := range cases {
		if got := HasWipPrefix(in); got != want {
			t.Errorf("HasWipPrefix(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestAutocommit_PrependsWipPrefix(t *testing.T) {
	dir := initWipRepo(t)

	// Stage a new file via Autocommit and verify the resulting
	// HEAD subject is prefixed with `wip!: `.
	writeFile(t, dir, "draft.txt", "wip body")

	// Autocommit shells out via the package's Run() which respects
	// the process cwd. Pin it so the test stays hermetic.
	wd, _ := os.Getwd()
	defer os.Chdir(wd)
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	if err := Autocommit(context.Background(), []string{"draft.txt"}, "draft change"); err != nil {
		t.Fatalf("Autocommit: %v", err)
	}
	got := headSubject(t, dir)
	want := "wip!: draft change"
	if got != want {
		t.Errorf("HEAD subject = %q, want %q", got, want)
	}
}

func TestAutocommit_DoesNotDoublePrefix(t *testing.T) {
	dir := initWipRepo(t)
	writeFile(t, dir, "draft2.txt", "more wip")

	wd, _ := os.Getwd()
	defer os.Chdir(wd)
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	if err := Autocommit(context.Background(), []string{"draft2.txt"}, "wip!: explicit"); err != nil {
		t.Fatalf("Autocommit: %v", err)
	}
	got := headSubject(t, dir)
	if got != "wip!: explicit" {
		t.Errorf("expected single prefix, got %q", got)
	}
}

func TestAutocommit_RejectsEmptyMessage(t *testing.T) {
	dir := initWipRepo(t)
	wd, _ := os.Getwd()
	defer os.Chdir(wd)
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	if err := Autocommit(context.Background(), nil, "   \n  "); err == nil {
		t.Error("expected empty-message error, got nil")
	}
}

func TestAutocommit_BlocksCoauthorTrailer(t *testing.T) {
	dir := initWipRepo(t)
	writeFile(t, dir, "draft3.txt", "x")
	wd, _ := os.Getwd()
	defer os.Chdir(wd)
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	err := Autocommit(context.Background(), []string{"draft3.txt"},
		"draft\n\nCo-Authored-By: bot")
	if err == nil {
		t.Error("expected coauthor block on wip commit, got nil")
	}
}
