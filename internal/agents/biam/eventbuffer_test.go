package biam

import (
	"sync"
	"testing"
	"time"
)

func TestEventBuffer_AppendAssignsMonotonicIDs(t *testing.T) {
	buf := NewTaskEventBuffer(8)
	a := buf.Append("t1", "task", []byte(`{"a":1}`))
	b := buf.Append("t1", "frame", []byte(`{"b":2}`))
	c := buf.Append("t1", "terminal", []byte(`{"c":3}`))
	if a.ID != 1 || b.ID != 2 || c.ID != 3 {
		t.Fatalf("expected IDs 1,2,3 got %d,%d,%d", a.ID, b.ID, c.ID)
	}
	if !buf.HasTerminal("t1") {
		t.Fatalf("HasTerminal: want true after kind=terminal")
	}
}

func TestEventBuffer_PerTaskIsolation(t *testing.T) {
	buf := NewTaskEventBuffer(8)
	buf.Append("t1", "task", []byte(`{}`))
	buf.Append("t2", "task", []byte(`{}`))
	t1, _ := buf.Since("t1", 0)
	t2, _ := buf.Since("t2", 0)
	if len(t1) != 1 || len(t2) != 1 {
		t.Fatalf("each ring should have 1; t1=%d t2=%d", len(t1), len(t2))
	}
	if t1[0].ID != 1 || t2[0].ID != 1 {
		t.Fatalf("per-task IDs reset; t1=%d t2=%d", t1[0].ID, t2[0].ID)
	}
}

func TestEventBuffer_SinceFiltersOlder(t *testing.T) {
	buf := NewTaskEventBuffer(8)
	for i := 0; i < 5; i++ {
		buf.Append("t1", "frame", []byte(`{}`))
	}
	out, dropped := buf.Since("t1", 3)
	if dropped {
		t.Fatalf("dropped should be false; ring has 5 items, asked since=3")
	}
	if len(out) != 2 {
		t.Fatalf("want 2 events (4,5) got %d", len(out))
	}
	if out[0].ID != 4 || out[1].ID != 5 {
		t.Fatalf("want IDs [4,5], got [%d,%d]", out[0].ID, out[1].ID)
	}
}

func TestEventBuffer_OverflowDropsOldestAndFlagsGap(t *testing.T) {
	buf := NewTaskEventBuffer(3)
	for i := 0; i < 5; i++ {
		buf.Append("t1", "frame", []byte(`{}`))
	}
	// Ring holds IDs 3,4,5. since=1 → asked for events after 1, but
	// the oldest retained ID is 3, so dropped should be true.
	out, dropped := buf.Since("t1", 1)
	if !dropped {
		t.Fatalf("dropped should be true; oldest retained=3 but since=1")
	}
	if len(out) != 3 {
		t.Fatalf("want 3 retained events, got %d", len(out))
	}
	if out[0].ID != 3 || out[2].ID != 5 {
		t.Fatalf("ring should hold 3..5, got %d..%d", out[0].ID, out[2].ID)
	}
}

func TestEventBuffer_SubscribeNotifiesOnAppend(t *testing.T) {
	buf := NewTaskEventBuffer(8)
	notify, unsub := buf.Subscribe("t1")
	defer unsub()

	go func() {
		time.Sleep(10 * time.Millisecond)
		buf.Append("t1", "frame", []byte(`{}`))
	}()

	select {
	case <-notify:
		// success
	case <-time.After(time.Second):
		t.Fatalf("subscribe channel never woke")
	}
}

func TestEventBuffer_UnsubscribeFreesSlot(t *testing.T) {
	buf := NewTaskEventBuffer(8)
	_, unsub := buf.Subscribe("t1")
	if got := buf.SubscriberCount("t1"); got != 1 {
		t.Fatalf("after subscribe want 1, got %d", got)
	}
	unsub()
	if got := buf.SubscriberCount("t1"); got != 0 {
		t.Fatalf("after unsubscribe want 0, got %d", got)
	}
}

// TestEventBuffer_ConcurrentAppendIsSafe exercises Append + Subscribe
// + Since under heavy parallelism. Race detector catches any
// unguarded slice / map access.
func TestEventBuffer_ConcurrentAppendIsSafe(t *testing.T) {
	buf := NewTaskEventBuffer(64)
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				buf.Append("t1", "frame", []byte(`{}`))
			}
		}()
	}
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_, _ = buf.Since("t1", uint64(j))
			}
		}()
	}
	wg.Wait()
}
