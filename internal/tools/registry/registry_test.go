package registry

import (
	"testing"

	"github.com/cogitave/clawtool/internal/search"
	"github.com/mark3labs/mcp-go/server"
)

func TestNew_EmptyManifest(t *testing.T) {
	m := New()
	if m == nil {
		t.Fatal("New returned nil")
	}
	if len(m.Specs()) != 0 {
		t.Errorf("fresh manifest has specs: %v", m.Specs())
	}
	if len(m.Names()) != 0 {
		t.Errorf("fresh manifest has names: %v", m.Names())
	}
}

func TestAppend_RoundTrip(t *testing.T) {
	m := New()
	m.Append(ToolSpec{
		Name:        "ExampleTool",
		Description: "An example",
		Keywords:    []string{"example", "test"},
		Category:    CategoryShell,
		Gate:        "Example",
	})
	if len(m.Specs()) != 1 {
		t.Fatalf("got %d specs, want 1", len(m.Specs()))
	}
	got := m.Specs()[0]
	if got.Name != "ExampleTool" {
		t.Errorf("Name drift: %q", got.Name)
	}
	if got.Category != CategoryShell {
		t.Errorf("Category drift: %q", got.Category)
	}
}

func TestAppend_DuplicateNamePanics(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic on duplicate Name")
		}
	}()
	m := New()
	m.Append(ToolSpec{Name: "Dup", Category: CategoryShell})
	m.Append(ToolSpec{Name: "Dup", Category: CategoryShell})
}

func TestAppend_EmptyNamePanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic on empty Name")
		}
	}()
	m := New()
	m.Append(ToolSpec{Category: CategoryShell})
}

func TestAppend_InvalidCategoryPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic on invalid Category")
		}
	}()
	m := New()
	m.Append(ToolSpec{Name: "X", Category: "wat"})
}

func TestSearchDocs_FiltersByGate(t *testing.T) {
	m := New()
	m.Append(ToolSpec{Name: "Always", Description: "Always-on", Category: CategoryShell, Gate: ""})
	m.Append(ToolSpec{Name: "Bash", Description: "shell", Category: CategoryShell, Gate: "Bash"})
	m.Append(ToolSpec{Name: "Edit", Description: "file edit", Category: CategoryFile, Gate: "Edit"})

	pred := func(name string) bool {
		// Bash off, Edit on.
		return name == "Edit"
	}
	docs := m.SearchDocs(pred)
	gotNames := map[string]bool{}
	for _, d := range docs {
		gotNames[d.Name] = true
	}
	if !gotNames["Always"] {
		t.Error("always-on (empty Gate) should pass through filter")
	}
	if gotNames["Bash"] {
		t.Error("Bash (gated off) should not appear")
	}
	if !gotNames["Edit"] {
		t.Error("Edit (gated on) should appear")
	}
}

func TestSearchDocs_NilPredicateIncludesEverything(t *testing.T) {
	m := New()
	m.Append(ToolSpec{Name: "A", Category: CategoryShell, Gate: "A"})
	m.Append(ToolSpec{Name: "B", Category: CategoryFile, Gate: "B"})
	docs := m.SearchDocs(nil)
	if len(docs) != 2 {
		t.Errorf("nil predicate should pass everything; got %d / 2", len(docs))
	}
}

func TestApply_CallsRegisterPerEnabledSpec(t *testing.T) {
	called := []string{}
	mkRegister := func(name string) RegisterFn {
		return func(_ *server.MCPServer, _ Runtime) {
			called = append(called, name)
		}
	}
	m := New()
	m.Append(ToolSpec{Name: "On", Category: CategoryShell, Gate: "On", Register: mkRegister("On")})
	m.Append(ToolSpec{Name: "Off", Category: CategoryShell, Gate: "Off", Register: mkRegister("Off")})
	m.Append(ToolSpec{Name: "AlwaysOn", Category: CategoryShell, Gate: "", Register: mkRegister("AlwaysOn")})
	m.Append(ToolSpec{Name: "NoRegister", Category: CategoryFile, Gate: ""}) // nil Register — silent skip

	pred := func(name string) bool { return name != "Off" }
	m.Apply(nil, Runtime{}, pred) // *server.MCPServer can be nil — our test fns ignore it

	want := []string{"On", "AlwaysOn"}
	if len(called) != len(want) {
		t.Fatalf("called = %v, want %v", called, want)
	}
	for i, n := range want {
		if called[i] != n {
			t.Errorf("called[%d] = %q, want %q", i, called[i], n)
		}
	}
}

func TestApply_NilPredicateRunsEverything(t *testing.T) {
	called := 0
	m := New()
	m.Append(ToolSpec{Name: "A", Category: CategoryShell, Gate: "A", Register: func(_ *server.MCPServer, _ Runtime) { called++ }})
	m.Append(ToolSpec{Name: "B", Category: CategoryFile, Gate: "", Register: func(_ *server.MCPServer, _ Runtime) { called++ }})
	m.Apply(nil, Runtime{}, nil)
	if called != 2 {
		t.Errorf("called = %d, want 2", called)
	}
}

func TestSortedNames_IsCaseInsensitive(t *testing.T) {
	m := New()
	for _, n := range []string{"Bash", "AgentNew", "Read", "Write"} {
		m.Append(ToolSpec{Name: n, Category: CategoryShell})
	}
	got := m.SortedNames()
	want := []string{"AgentNew", "Bash", "Read", "Write"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("SortedNames[%d] = %q, want %q (full=%v)", i, got[i], want[i], got)
		}
	}
}

func TestRuntime_FieldsAreOptional(t *testing.T) {
	// Runtime{} is the zero value; nothing should panic when a
	// register fn doesn't touch any of its fields.
	rt := Runtime{}
	if rt.Index != nil {
		t.Errorf("zero Runtime.Index = %v, want nil", rt.Index)
	}
}

// Compile-time guard: search.Doc / search.Index reachable from
// this package (no surprise import-cycle drift).
var _ = search.Doc{}
var _ = (*search.Index)(nil)
