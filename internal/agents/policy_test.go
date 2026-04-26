package agents

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/cogitave/clawtool/internal/config"
)

// erroringTransport always fails — used to exercise failover cascade.
type erroringTransport struct {
	family string
	calls  *atomic.Uint64
}

func (e erroringTransport) Family() string { return e.family }
func (e erroringTransport) Send(_ context.Context, _ string, _ map[string]any) (io.ReadCloser, error) {
	if e.calls != nil {
		e.calls.Add(1)
	}
	return nil, errors.New("upstream unavailable")
}

func TestExplicitPolicy_PicksRequested(t *testing.T) {
	all := []Agent{
		{Instance: "claude-personal", Family: "claude", Callable: true},
		{Instance: "claude-work", Family: "claude", Callable: true},
	}
	a, fb, err := explicitPolicy{}.Pick("claude-work", "", all)
	if err != nil {
		t.Fatal(err)
	}
	if a.Instance != "claude-work" {
		t.Errorf("got %q", a.Instance)
	}
	if len(fb) != 0 {
		t.Errorf("explicit should have no fallback; got %d", len(fb))
	}
}

func TestExplicitPolicy_RejectsEmpty(t *testing.T) {
	_, _, err := explicitPolicy{}.Pick("", "", nil)
	if err == nil {
		t.Error("explicit should reject empty requested")
	}
}

func TestRoundRobin_RotatesAcrossSameFamily(t *testing.T) {
	all := []Agent{
		{Instance: "claude-personal", Family: "claude", Callable: true},
		{Instance: "claude-work", Family: "claude", Callable: true},
	}
	p := &roundRobinPolicy{state: &roundRobinState{}}
	seen := []string{}
	for i := 0; i < 4; i++ {
		a, _, err := p.Pick("claude", "", all)
		if err != nil {
			t.Fatal(err)
		}
		seen = append(seen, a.Instance)
	}
	// Two distinct instances, four picks → each should appear at least
	// once and the sequence should alternate, not repeat the same one.
	count := map[string]int{}
	for _, s := range seen {
		count[s]++
	}
	if count["claude-personal"] == 0 || count["claude-work"] == 0 {
		t.Errorf("round-robin should hit both instances; got %v", count)
	}
}

func TestRoundRobin_PinnedInstanceWins(t *testing.T) {
	all := []Agent{
		{Instance: "claude-personal", Family: "claude", Callable: true},
		{Instance: "claude-work", Family: "claude", Callable: true},
	}
	p := &roundRobinPolicy{state: &roundRobinState{}}
	a, _, err := p.Pick("claude-personal", "", all)
	if err != nil {
		t.Fatal(err)
	}
	if a.Instance != "claude-personal" {
		t.Errorf("pinned instance should win over rotation; got %q", a.Instance)
	}
}

func TestRoundRobin_NoCandidates(t *testing.T) {
	p := &roundRobinPolicy{state: &roundRobinState{}}
	_, _, err := p.Pick("codex", "", nil)
	if err == nil {
		t.Error("expected error when family has no callable instances")
	}
}

func TestFailoverPolicy_ReturnsChain(t *testing.T) {
	all := []Agent{
		{Instance: "claude-personal", Family: "claude", Callable: true, FailoverTo: []string{"claude-work", "codex1"}},
		{Instance: "claude-work", Family: "claude", Callable: true},
		{Instance: "codex1", Family: "codex", Callable: true},
	}
	primary, fb, err := failoverPolicy{}.Pick("claude-personal", "", all)
	if err != nil {
		t.Fatal(err)
	}
	if primary.Instance != "claude-personal" {
		t.Errorf("primary: got %q", primary.Instance)
	}
	if len(fb) != 2 || fb[0].Instance != "claude-work" || fb[1].Instance != "codex1" {
		t.Errorf("fallback chain mismatch: %+v", fb)
	}
}

func TestFailoverPolicy_SkipsNonCallableFallback(t *testing.T) {
	all := []Agent{
		{Instance: "claude-personal", Family: "claude", Callable: true, FailoverTo: []string{"claude-work", "codex1"}},
		{Instance: "claude-work", Family: "claude", Callable: false},
		{Instance: "codex1", Family: "codex", Callable: true},
	}
	_, fb, err := failoverPolicy{}.Pick("claude-personal", "", all)
	if err != nil {
		t.Fatal(err)
	}
	if len(fb) != 1 || fb[0].Instance != "codex1" {
		t.Errorf("non-callable fallback should be skipped; got %+v", fb)
	}
}

func TestTagRoutedPolicy_PicksMatchingInstance(t *testing.T) {
	all := []Agent{
		{Instance: "claude-fast", Family: "claude", Callable: true, Tags: []string{"fast"}},
		{Instance: "codex-deep", Family: "codex", Callable: true, Tags: []string{"long-context"}},
	}
	a, _, err := tagRoutedPolicy{}.Pick("", "long-context", all)
	if err != nil {
		t.Fatal(err)
	}
	if a.Instance != "codex-deep" {
		t.Errorf("tag-routed picked wrong instance: %q", a.Instance)
	}
}

func TestTagRoutedPolicy_CaseInsensitive(t *testing.T) {
	all := []Agent{{Instance: "x", Family: "claude", Callable: true, Tags: []string{"FAST"}}}
	a, _, err := tagRoutedPolicy{}.Pick("", "fast", all)
	if err != nil {
		t.Fatal(err)
	}
	if a.Instance != "x" {
		t.Errorf("tag match should be case-insensitive")
	}
}

func TestTagRoutedPolicy_NoMatchErrors(t *testing.T) {
	all := []Agent{{Instance: "x", Family: "claude", Callable: true, Tags: []string{"fast"}}}
	_, _, err := tagRoutedPolicy{}.Pick("", "long-context", all)
	if err == nil {
		t.Error("expected error when no instance carries the tag")
	}
}

func TestTagRoutedPolicy_RejectsEmptyTag(t *testing.T) {
	_, _, err := tagRoutedPolicy{}.Pick("", "", nil)
	if err == nil {
		t.Error("expected error when tag is empty")
	}
}

func TestPickPolicy_ResolvesModes(t *testing.T) {
	rr := &roundRobinState{}
	cases := map[string]string{
		"":              "explicit",
		"explicit":      "explicit",
		"round-robin":   "round-robin",
		"ROUND_ROBIN":   "round-robin",
		"failover":      "failover",
		"tag-routed":    "tag-routed",
		"tag":           "tag-routed",
		"unknown-thing": "explicit",
	}
	for mode, want := range cases {
		got := pickPolicy(mode, rr)
		switch want {
		case "explicit":
			if _, ok := got.(explicitPolicy); !ok {
				t.Errorf("mode %q expected explicitPolicy, got %T", mode, got)
			}
		case "round-robin":
			if _, ok := got.(*roundRobinPolicy); !ok {
				t.Errorf("mode %q expected *roundRobinPolicy, got %T", mode, got)
			}
		case "failover":
			if _, ok := got.(failoverPolicy); !ok {
				t.Errorf("mode %q expected failoverPolicy, got %T", mode, got)
			}
		case "tag-routed":
			if _, ok := got.(tagRoutedPolicy); !ok {
				t.Errorf("mode %q expected tagRoutedPolicy, got %T", mode, got)
			}
		}
	}
}

// failoverSupervisor wires the supervisor with a transport that errors
// on the primary family and a fake-OK transport on the fallback family.
// The dispatch chain should fall through and return the fallback's body.
func TestSupervisor_FailoverCascade(t *testing.T) {
	primaryCalls := &atomic.Uint64{}
	cfg := config.Config{
		Agents: map[string]config.AgentConfig{
			"claude-personal": {Family: "claude", FailoverTo: []string{"codex1"}},
			"codex1":          {Family: "codex"},
		},
	}
	tmp := t.TempDir()
	binaryOnPath = func(name string) bool { return true }
	t.Cleanup(func() {
		binaryOnPath = func(name string) bool {
			_, err := lookPath(name)
			return err == nil
		}
	})
	s := &supervisor{
		loadConfig: func() (config.Config, error) { return cfg, nil },
		transports: map[string]Transport{
			"claude": erroringTransport{family: "claude", calls: primaryCalls},
			"codex":  fakeTransport{family: "codex", body: "codex-out"},
		},
		stickyPath: tmp + "/sticky",
		rrState:    &roundRobinState{},
	}
	// dispatch.mode is empty; explicit policy doesn't return a chain,
	// so we test failover by setting mode = "failover".
	cfg.Dispatch.Mode = "failover"
	s.loadConfig = func() (config.Config, error) { return cfg, nil }

	rc, err := s.Send(context.Background(), "claude-personal", "hi", nil)
	if err != nil {
		t.Fatalf("expected fallback to succeed, got %v", err)
	}
	defer rc.Close()
	body, _ := io.ReadAll(rc)
	if !strings.HasPrefix(string(body), "codex-out|") {
		t.Errorf("expected fallback's output, got %q", body)
	}
	if primaryCalls.Load() == 0 {
		t.Error("primary should have been attempted before falling over")
	}
}

func TestSupervisor_TagRoutedDispatch(t *testing.T) {
	cfg := config.Config{
		Agents: map[string]config.AgentConfig{
			"fast-claude": {Family: "claude", Tags: []string{"fast"}},
			"deep-codex":  {Family: "codex", Tags: []string{"long-context"}},
		},
	}
	binaryOnPath = func(name string) bool { return true }
	t.Cleanup(func() {
		binaryOnPath = func(name string) bool {
			_, err := lookPath(name)
			return err == nil
		}
	})
	s := &supervisor{
		loadConfig: func() (config.Config, error) { return cfg, nil },
		transports: map[string]Transport{
			"claude": fakeTransport{family: "claude", body: "claude-out"},
			"codex":  fakeTransport{family: "codex", body: "codex-out"},
		},
		stickyPath: t.TempDir() + "/sticky",
		rrState:    &roundRobinState{},
	}
	rc, err := s.Send(context.Background(), "", "summarise", map[string]any{"tag": "long-context"})
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	body, _ := io.ReadAll(rc)
	if !strings.HasPrefix(string(body), "codex-out|") {
		t.Errorf("tag dispatch should hit codex-out instance; got %q", body)
	}
}
