package biam

import (
	"sync"
	"testing"
	"time"
)

func TestNotifier_PublishWakesSubscriber(t *testing.T) {
	Notifier.ResetForTest()

	sub := Notifier.Subscribe("t1")
	defer sub.Cancel()

	go func() {
		time.Sleep(20 * time.Millisecond)
		Notifier.Publish(Task{TaskID: "t1", Status: TaskDone})
	}()

	select {
	case got := <-sub.Ch:
		if got.TaskID != "t1" {
			t.Errorf("got task_id %q, want t1", got.TaskID)
		}
		if got.Status != TaskDone {
			t.Errorf("got status %q, want done", got.Status)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("subscriber did not wake within 500ms")
	}
}

func TestNotifier_CancelRemovesSlot(t *testing.T) {
	Notifier.ResetForTest()

	sub := Notifier.Subscribe("t2")
	if got := Notifier.SubsCount("t2"); got != 1 {
		t.Errorf("after Subscribe, SubsCount=%d, want 1", got)
	}
	sub.Cancel()
	if got := Notifier.SubsCount("t2"); got != 0 {
		t.Errorf("after Cancel, SubsCount=%d, want 0", got)
	}
}

func TestNotifier_MultipleSubscribers(t *testing.T) {
	Notifier.ResetForTest()

	const n = 5
	subs := make([]*Sub, n)
	for i := range subs {
		subs[i] = Notifier.Subscribe("t3")
	}

	go Notifier.Publish(Task{TaskID: "t3", Status: TaskDone})

	var wg sync.WaitGroup
	for _, s := range subs {
		wg.Add(1)
		go func(sub *Sub) {
			defer wg.Done()
			defer sub.Cancel()
			select {
			case <-sub.Ch:
			case <-time.After(500 * time.Millisecond):
				t.Error("subscriber did not wake")
			}
		}(s)
	}
	wg.Wait()
}

func TestNotifier_PublishNoSubscribersIsNoop(t *testing.T) {
	Notifier.ResetForTest()
	// Should not panic, should not block.
	Notifier.Publish(Task{TaskID: "ghost", Status: TaskDone})
}

func TestNotifier_SubscribeAfterPublishNeverFires(t *testing.T) {
	// Documents the expected behaviour: Notifier is edge-triggered.
	// Already-fired publishes don't replay. Callers handle the
	// already-terminal case by checking the store FIRST (the
	// TaskNotify tool does exactly this).
	Notifier.ResetForTest()
	Notifier.Publish(Task{TaskID: "early", Status: TaskDone})

	sub := Notifier.Subscribe("early")
	defer sub.Cancel()

	select {
	case got := <-sub.Ch:
		t.Errorf("subscriber unexpectedly received %+v after a missed publish", got)
	case <-time.After(150 * time.Millisecond):
		// Expected — no replay.
	}
}
