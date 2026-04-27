package agents

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/time/rate"
)

func TestParseRate_Forms(t *testing.T) {
	cases := []struct {
		in      string
		want    rate.Limit
		wantErr bool
	}{
		{"", 0, false},       // disabled
		{"30/m", 0.5, false}, // 30 per minute = 0.5/s
		{"5/s", 5, false},    // 5 per second
		{"1000/h", 1000.0 / 3600, false},
		{"60/1m", 1, false}, // explicit "1m"
		{"abc", 0, true},
		{"30/", 0, true},
		{"/m", 0, true},
		{"30/0s", 0, true},
	}
	for _, c := range cases {
		got, err := parseRate(c.in)
		if (err != nil) != c.wantErr {
			t.Errorf("parseRate(%q) err=%v wantErr=%v", c.in, err, c.wantErr)
			continue
		}
		if !c.wantErr && (got < c.want*0.999 || got > c.want*1.001) {
			t.Errorf("parseRate(%q) = %v, want ≈%v", c.in, got, c.want)
		}
	}
}

func TestLimiter_DisabledIsNoop(t *testing.T) {
	l, err := newDispatchLimiter("", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	release, err := l.acquire(context.Background(), "x")
	if err != nil {
		t.Fatalf("disabled acquire should not error: %v", err)
	}
	release() // must not panic
}

func TestLimiter_RateBucketBlocks(t *testing.T) {
	// 10/s rate, burst 1: second acquire within ~100ms should wait.
	l, err := newDispatchLimiter("10/s", 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	r1, err := l.acquire(context.Background(), "x")
	if err != nil {
		t.Fatal(err)
	}
	r1()
	start := time.Now()
	r2, err := l.acquire(context.Background(), "x")
	if err != nil {
		t.Fatal(err)
	}
	r2()
	elapsed := time.Since(start)
	if elapsed < 50*time.Millisecond {
		t.Errorf("bucket should have blocked ~100ms; got %v", elapsed)
	}
}

func TestLimiter_PerInstanceIndependent(t *testing.T) {
	l, _ := newDispatchLimiter("1/s", 1, 0)
	// First acquire on "a" eats its token.
	r, _ := l.acquire(context.Background(), "a")
	r()
	// Acquire on "b" should NOT block — different bucket.
	start := time.Now()
	r2, err := l.acquire(context.Background(), "b")
	if err != nil {
		t.Fatal(err)
	}
	r2()
	if time.Since(start) > 50*time.Millisecond {
		t.Error("per-instance buckets should be independent")
	}
}

func TestLimiter_Concurrency(t *testing.T) {
	l, _ := newDispatchLimiter("", 0, 2) // unlimited rate, max 2 concurrent
	var inFlight int32
	var maxSeen int32
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := l.acquire(context.Background(), "x")
			if err != nil {
				t.Errorf("acquire: %v", err)
				return
			}
			defer r()
			cur := atomic.AddInt32(&inFlight, 1)
			for {
				old := atomic.LoadInt32(&maxSeen)
				if cur <= old || atomic.CompareAndSwapInt32(&maxSeen, old, cur) {
					break
				}
			}
			time.Sleep(10 * time.Millisecond)
			atomic.AddInt32(&inFlight, -1)
		}()
	}
	wg.Wait()
	if maxSeen > 2 {
		t.Errorf("max concurrent should be 2; saw %d", maxSeen)
	}
}

func TestLimiter_CtxCancellation(t *testing.T) {
	l, _ := newDispatchLimiter("1/h", 1, 0) // very slow bucket
	r, _ := l.acquire(context.Background(), "x")
	r()
	// Second acquire on the same instance should block forever; ctx
	// cancel surfaces as an error.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := l.acquire(ctx, "x"); err == nil {
		t.Error("expected ctx-cancel error from drained bucket")
	}
}
