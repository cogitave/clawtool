package biam

import (
	"strings"
	"testing"
	"time"
)

// drainFrames pulls frames off ch until either count is reached or
// the deadline expires. Returns whatever it managed to collect.
func drainFrames(ch <-chan StreamFrame, count int, deadline time.Duration) []StreamFrame {
	out := make([]StreamFrame, 0, count)
	timer := time.NewTimer(deadline)
	defer timer.Stop()
	for len(out) < count {
		select {
		case f := <-ch:
			out = append(out, f)
		case <-timer.C:
			return out
		}
	}
	return out
}

func TestReadCappedBroadcast_EmitsOneFramePerLine(t *testing.T) {
	Watch.ResetWatchForTest()
	frames, unsub := Watch.SubscribeFrames()
	defer unsub()

	input := "step 1\nstep 2\nstep 3\n"
	body, truncated := readCappedBroadcast(strings.NewReader(input), 1024, "task-A", "codex-2")

	if body != input {
		t.Errorf("body mismatch: got %q want %q", body, input)
	}
	if truncated {
		t.Errorf("expected not truncated, got truncated=true")
	}

	got := drainFrames(frames, 3, time.Second)
	if len(got) != 3 {
		t.Fatalf("expected 3 frames, got %d: %+v", len(got), got)
	}
	for i, want := range []string{"step 1", "step 2", "step 3"} {
		if got[i].Line != want {
			t.Errorf("frame %d line: got %q want %q", i, got[i].Line, want)
		}
		if got[i].TaskID != "task-A" {
			t.Errorf("frame %d TaskID: got %q want task-A", i, got[i].TaskID)
		}
		if got[i].Agent != "codex" {
			t.Errorf("frame %d Agent: got %q want codex (family stripped from codex-2)", i, got[i].Agent)
		}
		if got[i].Kind != "stdout" {
			t.Errorf("frame %d Kind: got %q want stdout", i, got[i].Kind)
		}
	}
}

func TestReadCappedBroadcast_HandlesTrailingLineWithoutNewline(t *testing.T) {
	Watch.ResetWatchForTest()
	frames, unsub := Watch.SubscribeFrames()
	defer unsub()

	input := "first\nlast-no-newline"
	body, _ := readCappedBroadcast(strings.NewReader(input), 1024, "t", "claude")
	if body != input {
		t.Errorf("body mismatch: got %q want %q", body, input)
	}

	got := drainFrames(frames, 2, time.Second)
	if len(got) != 2 {
		t.Fatalf("expected 2 frames, got %d", len(got))
	}
	if got[0].Line != "first" || got[1].Line != "last-no-newline" {
		t.Errorf("lines wrong: %q / %q", got[0].Line, got[1].Line)
	}
}

func TestReadCappedBroadcast_TruncatesBodyButKeepsBroadcasting(t *testing.T) {
	Watch.ResetWatchForTest()
	frames, unsub := Watch.SubscribeFrames()
	defer unsub()

	// Five 10-byte lines = 50 bytes total. Cap at 25 — body keeps
	// the first ~2.5 lines, but every line still goes out as a
	// frame so the live view stays accurate.
	input := "0123456789\n0123456789\n0123456789\n0123456789\n0123456789\n"
	body, truncated := readCappedBroadcast(strings.NewReader(input), 25, "t", "gemini")

	if !truncated {
		t.Errorf("expected truncated=true at cap 25 over 55 bytes")
	}
	if len(body) != 25 {
		t.Errorf("body should be exactly 25 bytes when truncating mid-line; got %d (%q)", len(body), body)
	}

	got := drainFrames(frames, 5, time.Second)
	if len(got) != 5 {
		t.Fatalf("expected 5 frames despite body truncation, got %d", len(got))
	}
}

func TestReadCappedBroadcast_EmptyReaderEmitsNoFrames(t *testing.T) {
	Watch.ResetWatchForTest()
	frames, unsub := Watch.SubscribeFrames()
	defer unsub()

	body, truncated := readCappedBroadcast(strings.NewReader(""), 1024, "t", "hermes")
	if body != "" {
		t.Errorf("expected empty body, got %q", body)
	}
	if truncated {
		t.Errorf("empty input should not flag truncation")
	}

	select {
	case f := <-frames:
		t.Errorf("expected zero frames, got %+v", f)
	case <-time.After(50 * time.Millisecond):
		// good — no frame arrived
	}
}
