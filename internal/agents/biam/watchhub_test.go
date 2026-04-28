package biam

import (
	"path/filepath"
	"testing"
	"time"
)

func TestWatchHub_BroadcastFanOutsToAllSubscribers(t *testing.T) {
	hub := &WatchHub{subs: map[*watchSub]struct{}{}}
	chA, unsubA := hub.Subscribe()
	chB, unsubB := hub.Subscribe()
	defer unsubA()
	defer unsubB()

	hub.Broadcast(Task{TaskID: "t1", Status: TaskActive})

	for _, ch := range []<-chan Task{chA, chB} {
		select {
		case got := <-ch:
			if got.TaskID != "t1" {
				t.Errorf("expected t1, got %+v", got)
			}
		case <-time.After(time.Second):
			t.Fatal("subscriber didn't receive broadcast")
		}
	}
}

func TestWatchHub_UnsubscribeRemovesSlot(t *testing.T) {
	hub := &WatchHub{subs: map[*watchSub]struct{}{}}
	_, unsub := hub.Subscribe()
	if hub.SubsCount() != 1 {
		t.Fatalf("expected 1 sub, got %d", hub.SubsCount())
	}
	unsub()
	if hub.SubsCount() != 0 {
		t.Fatalf("expected 0 subs after unsub, got %d", hub.SubsCount())
	}
	// Idempotent — second call must not panic / underflow.
	unsub()
	if hub.SubsCount() != 0 {
		t.Errorf("idempotent unsub broke the count")
	}
}

func TestWatchHub_SystemBroadcastFanOut(t *testing.T) {
	hub := &WatchHub{
		subs:   map[*watchSub]struct{}{},
		frames: map[*frameSub]struct{}{},
		system: map[*systemSub]struct{}{},
	}
	chA, unsubA := hub.SubscribeSystem()
	chB, unsubB := hub.SubscribeSystem()
	defer unsubA()
	defer unsubB()

	hub.BroadcastSystem(SystemNotification{
		Kind:  "update_available",
		Title: "clawtool 0.22.5 → 0.22.6",
	})

	for i, ch := range []<-chan SystemNotification{chA, chB} {
		select {
		case got := <-ch:
			if got.Kind != "update_available" || got.Title == "" {
				t.Errorf("subscriber %d got %+v", i, got)
			}
		case <-time.After(time.Second):
			t.Fatalf("subscriber %d didn't receive system notification", i)
		}
	}
}

func TestWatchHub_SystemUnsubscribeFreesSlot(t *testing.T) {
	hub := &WatchHub{
		subs:   map[*watchSub]struct{}{},
		frames: map[*frameSub]struct{}{},
		system: map[*systemSub]struct{}{},
	}
	_, unsub := hub.SubscribeSystem()
	if hub.SystemSubsCount() != 1 {
		t.Fatalf("expected 1 system sub, got %d", hub.SystemSubsCount())
	}
	unsub()
	if hub.SystemSubsCount() != 0 {
		t.Fatalf("expected 0 system subs after unsub, got %d", hub.SystemSubsCount())
	}
	unsub() // idempotent
	if hub.SystemSubsCount() != 0 {
		t.Errorf("idempotent unsub broke count")
	}
}

func TestWatchHub_BroadcastDropsOnSlowSubscriber(t *testing.T) {
	hub := &WatchHub{subs: map[*watchSub]struct{}{}}
	_, unsub := hub.Subscribe() // never drained
	defer unsub()

	// Cap is 32 — fire more than that to confirm drops don't block.
	for i := 0; i < 100; i++ {
		hub.Broadcast(Task{TaskID: "t", Status: TaskActive})
	}
	// If Broadcast had blocked, the test would time out via go test.
}

// TestStoreHook_FiresAfterStateMutation confirms the store wires
// SetTaskHook to every successful SetTaskStatus call. Critical for
// the watchsocket: missing hook = silent watcher starvation.
func TestStoreHook_FiresAfterStateMutation(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(filepath.Join(dir, "biam.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	got := make(chan string, 4)
	store.SetTaskHook(func(taskID string) {
		got <- taskID
	})

	ctx := t.Context()
	if err := store.CreateTask(ctx, "t1", "tester", "claude"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetTaskStatus(ctx, "t1", TaskActive, ""); err != nil {
		t.Fatal(err)
	}
	if err := store.SetTaskStatus(ctx, "t1", TaskDone, "summary"); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 2; i++ {
		select {
		case id := <-got:
			if id != "t1" {
				t.Errorf("hook fired for wrong task: %q", id)
			}
		case <-time.After(time.Second):
			t.Fatalf("hook didn't fire for transition #%d", i+1)
		}
	}
}
