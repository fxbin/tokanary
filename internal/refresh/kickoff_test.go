package refresh

import (
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// reset forgets the rate-limit and in-flight bookkeeping for a root, so cases
// cannot leak into each other. It lives here rather than in kickoff.go because
// nothing in the pipeline needs it, and an exported function no caller uses is
// exactly the dead API this cleanup removed elsewhere.
func reset(root string) {
	mu.Lock()
	defer mu.Unlock()
	delete(inFlight, root)
	delete(lastEnd, root)
}

// withStub swaps the refresh implementation and the clock, and isolates the
// per-root bookkeeping so cases cannot leak into each other.
func withStub(t *testing.T, root string) *atomic.Int64 {
	t.Helper()
	var calls atomic.Int64
	prevTouch, prevClock := touch, clock
	touch = func(string) (Result, error) { calls.Add(1); return Result{}, nil }
	clock = time.Now
	reset(root)
	t.Cleanup(func() {
		touch, clock = prevTouch, prevClock
		reset(root)
	})
	return &calls
}

func withWarehouse(t *testing.T, root string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, ".cache"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".cache", "tokanary.sqlite"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestKickoffCollapsesConcurrentCallers is the memory fix stated as a test.
//
// Each refresh of the largest adapter measured 2.6 GB, and the read path used
// to start one per request: three requests meant three side by side, 10.5 GB,
// and a crash when the machine refused. Only one refresh may exist at a time,
// no matter how many requests arrive.
func TestKickoffCollapsesConcurrentCallers(t *testing.T) {
	root := t.TempDir()
	withWarehouse(t, root)
	calls := withStub(t, root)

	release := make(chan struct{})
	prevTouch := touch
	touch = func(string) (Result, error) {
		calls.Add(1)
		<-release // hold the refresh open so every caller has to pile up
		return Result{}, nil
	}
	t.Cleanup(func() { touch = prevTouch })

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); Kickoff(root) }()
	}
	// Give the goroutines time to all arrive while the first refresh is stuck.
	time.Sleep(150 * time.Millisecond)
	close(release)
	wg.Wait()

	// Let the in-flight bookkeeping settle before asserting.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		busy := inFlight[root]
		mu.Unlock()
		if !busy {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("%d refreshes ran for 16 concurrent callers, want 1", got)
	}
}

// TestKickoffHonoursTheIntervalFloor covers the second half of the storm:
// without a floor, a refresh that completes between two clicks invites another,
// and switching ranges in the UI turns into a loop of full re-parses.
func TestKickoffHonoursTheIntervalFloor(t *testing.T) {
	root := t.TempDir()
	withWarehouse(t, root)
	calls := withStub(t, root)

	Kickoff(root)
	settle(t, root)
	if got := calls.Load(); got != 1 {
		t.Fatalf("first Kickoff ran %d refreshes, want 1", got)
	}

	// Ten more inside the floor, each pretending a lot of time passed.
	base := clock()
	for i := 0; i < 10; i++ {
		clock = func() time.Time { return base.Add(time.Duration(i) * time.Second) }
		Kickoff(root)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("refreshes inside the floor: got %d, want 1", got)
	}

	// Past the floor, the next request is served again.
	clock = func() time.Time { return base.Add(2 * minRefreshInterval) }
	Kickoff(root)
	settle(t, root)
	if got := calls.Load(); got != 2 {
		t.Fatalf("after the floor: got %d refreshes, want 2", got)
	}
}

// TestKickoffBlocksWhenThereIsNoWarehouse keeps the first run working. With no
// warehouse there is nothing to render, so a non-blocking kickoff would answer
// the page with an error the user cannot act on until a refresh happened to
// finish - the "run tokanary refresh" hint on an otherwise fresh install.
func TestKickoffBlocksWhenThereIsNoWarehouse(t *testing.T) {
	root := t.TempDir()
	calls := withStub(t, root)

	Kickoff(root)

	if got := calls.Load(); got != 1 {
		t.Fatalf("first run ran %d refreshes, want 1", got)
	}
	// Synchronous: no settling allowed, the caller must already have the data.
	mu.Lock()
	busy := inFlight[root]
	mu.Unlock()
	if busy {
		t.Error("Kickoff returned while its refresh was still in flight on a first run")
	}
}

// TestKickoffIgnoresAnEmptyRoot keeps a misconfigured host from creating a
// refresh that operates on the process working directory.
func TestKickoffIgnoresAnEmptyRoot(t *testing.T) {
	calls := withStub(t, "")
	Kickoff("")
	if got := calls.Load(); got != 0 {
		t.Fatalf("empty root ran %d refreshes, want 0", got)
	}
}

// settle waits for the background refresh to finish and clear its slot.
func settle(t *testing.T, root string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		busy := inFlight[root]
		mu.Unlock()
		if !busy {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("background refresh never finished")
}
