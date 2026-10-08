package refresh

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// The read path must not do the work.
//
// Touch re-parses every adapter whose sources moved, and pi rewrites its
// database on every turn, so on a machine in active use it is a full rebuild:
// measured 2.6 GB and 35s for the largest adapter alone. Calling it per request
// meant a page that fired three requests at once ran three of those side by side
// - 10.5 GB peak, every request waiting a minute, and a crash once the machine
// ran out. The numbers were also wrong in a quieter way: the read blocked on the
// very work it was trying to display.
//
// So the read path asks for a refresh and returns what the warehouse already
// holds. Three mechanisms keep that honest:
//
//   - single-flight: concurrent callers share one refresh instead of starting
//     one each, which is what bounds memory;
//   - an interval floor: without it, a refresh that finishes between two clicks
//     invites the next one, and rapid range switching turns into a storm;
//   - a blocking first run: with no warehouse there is nothing to serve, so the
//     one request that would otherwise render an empty page does the work.
//
// Staleness is bounded by the interval floor and is already visible in the
// payload: meta.generatedAt is the warehouse's own timestamp.

// minRefreshInterval is the floor between two refreshes started by the read
// path. Fifteen seconds is invisible on a usage dashboard and bounds the work
// to something a background goroutine does at leisure.
const minRefreshInterval = 15 * time.Second

// touch is the indirection tests use to observe how often a refresh runs.
var touch = Touch

// clock is the interval gate's time source, indirected for the same reason.
var clock = time.Now

var (
	mu       sync.Mutex
	inFlight = map[string]bool{}
	lastEnd  = map[string]time.Time{}
)

// Kickoff requests a refresh of repoRoot and returns without waiting for it.
//
// It is safe to call from every request: the work is shared, rate-limited, and
// its failures are reported on stderr rather than to a caller that has already
// been answered.
func Kickoff(repoRoot string) {
	if repoRoot == "" {
		return
	}
	// Nothing to display yet, so there is nothing to be non-blocking about.
	if !hasWarehouse(repoRoot) {
		run(repoRoot)
		return
	}
	mu.Lock()
	switch {
	case inFlight[repoRoot]:
		mu.Unlock()
		return
	case clock().Sub(lastEnd[repoRoot]) < minRefreshInterval:
		mu.Unlock()
		return
	}
	inFlight[repoRoot] = true
	mu.Unlock()

	go run(repoRoot)
}

// run performs one refresh and records when it finished, so the next caller can
// honour the interval floor.
func run(repoRoot string) {
	defer func() {
		mu.Lock()
		delete(inFlight, repoRoot)
		lastEnd[repoRoot] = clock()
		mu.Unlock()
	}()
	if _, err := touch(repoRoot); err != nil {
		fmt.Fprintf(os.Stderr, "[warn] live refresh: %v\n", err)
	}
}

// hasWarehouse reports whether there is anything to serve without refreshing.
func hasWarehouse(repoRoot string) bool {
	_, err := os.Stat(filepath.Join(repoRoot, ".cache", "tokanary.sqlite"))
	return err == nil
}
