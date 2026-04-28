package biam

import (
	"strings"
	"testing"
)

// TestSummary_PlainTextFirstLine confirms non-NDJSON bodies fall
// through to the legacy first-line-up-to-200 behaviour. This is
// the path claude -p uses (raw text bodies, not stream-json).
func TestSummary_PlainTextFirstLine(t *testing.T) {
	cases := map[string]string{
		"hello world":                       "hello world",
		"hello world\nmore lines after":     "hello world",
		"":                                  "",
		"single line, no newline at all":    "single line, no newline at all",
	}
	for in, want := range cases {
		if got := summary(in); got != want {
			t.Errorf("summary(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestSummary_PlainTextClipsAt200 confirms the 200-char clip kicks
// in for long single-line bodies (e.g. an error message that fills
// a paragraph without newlines).
func TestSummary_PlainTextClipsAt200(t *testing.T) {
	body := ""
	for i := 0; i < 250; i++ {
		body += "x"
	}
	got := summary(body)
	// "…" is 3 bytes UTF-8; 200 ASCII bytes + "…" = 203 bytes.
	if len(got) != 200+len("…") {
		t.Errorf("expected %d bytes (200 ASCII + ellipsis), got %d", 200+len("…"), len(got))
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("expected trailing ellipsis, got tail %q", got[len(got)-10:])
	}
}

// TestSummary_NDJSONExtractsAgentMessage is the regression guard
// for the operator's "task list shows {thread.started, ...}"
// complaint. The summary should walk the NDJSON tail and lift the
// last `agent_message` text instead of returning the meaningless
// first-line header.
func TestSummary_NDJSONExtractsAgentMessage(t *testing.T) {
	body := `{"type":"thread.started","thread_id":"019dd3f3-72cb"}
{"type":"turn.started"}
{"type":"item.completed","item":{"id":"item_0","type":"agent_message","text":"I'll inspect the local repo first."}}
{"type":"item.started","item":{"id":"item_1","type":"command_execution","command":"/bin/bash -lc 'find ...'"}}
{"type":"item.completed","item":{"id":"item_2","type":"agent_message","text":"Final answer: use SessionStart hook bundled in plugin hooks/hooks.json."}}
{"type":"turn.completed","usage":{"input_tokens":1402928}}`

	got := summary(body)
	want := "Final answer: use SessionStart hook bundled in plugin hooks/hooks.json."
	if got != want {
		t.Errorf("summary should pick the LAST agent_message\n  got:  %q\n  want: %q", got, want)
	}
}

// TestSummary_NDJSONNoAgentMessageFallsThrough confirms NDJSON
// bodies without any agent_message item fall back to first-line
// behaviour rather than returning empty. Some failure streams
// only emit error events.
func TestSummary_NDJSONNoAgentMessageFallsThrough(t *testing.T) {
	body := `{"type":"thread.started","thread_id":"019dd3f3"}
{"type":"turn.failed","error":{"message":"content policy"}}`
	got := summary(body)
	want := `{"type":"thread.started","thread_id":"019dd3f3"}`
	if got != want {
		t.Errorf("no-agent-message body should fall through to first-line\n  got:  %q\n  want: %q", got, want)
	}
}

// TestSummary_NDJSONClipsLongAgentMessage confirms a giant final
// agent_message is still clipped to the 200-char budget. Rare in
// practice (most replies fit) but the contract is the same as
// plain text — task list rows have a fixed visual width.
func TestSummary_NDJSONClipsLongAgentMessage(t *testing.T) {
	long := ""
	for i := 0; i < 300; i++ {
		long += "x"
	}
	body := `{"type":"thread.started"}` + "\n" +
		`{"type":"item.completed","item":{"type":"agent_message","text":"` + long + `"}}`
	got := summary(body)
	if len(got) != 200+len("…") {
		t.Errorf("expected %d bytes (200 ASCII + ellipsis), got %d", 200+len("…"), len(got))
	}
}
