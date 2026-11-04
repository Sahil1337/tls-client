package main

import (
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"
)

// A stand-in host function: never called as C, notifyCall is swapped.
var fakeFn byte

// host stands in for a host that drains with waitNext(0) when called. With
// lag it sleeps first, like a busy JS main thread.
type host struct {
	calls, got atomic.Int64
	inCall     atomic.Int32
	overlap    atomic.Bool
	lag        time.Duration
	// Runs once the drain finds nothing, before the call returns.
	afterDrain func()
}

func (h *host) call(unsafe.Pointer) {
	if h.inCall.Add(1) > 1 {
		h.overlap.Store(true)
	}
	defer h.inCall.Add(-1)
	h.calls.Add(1)
	if h.lag > 0 {
		time.Sleep(h.lag)
	}
	for {
		select {
		case <-completions: // what waitNext(0) does
			queued.Add(-1)
			h.got.Add(1)
		default:
			if h.afterDrain != nil {
				h.afterDrain()
			}
			return
		}
	}
}

// install points the notifier at h; undone when the test ends.
func install(t *testing.T, h *host) {
	t.Helper()
	notifyCall = h.call
	setNotifyFn(unsafe.Pointer(&fakeFn))
	t.Cleanup(func() {
		setNotifyFn(nil)
		// Let a call already under way finish before the next test swaps notifyCall.
		time.Sleep(20 * time.Millisecond)
	})
}

// finish queues one completion the way requestAsync's goroutine does.
func finish() {
	completions <- completion{}
	queued.Add(1)
	notifyRaise()
}

func waitFor(t *testing.T, h *host, want int64) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for h.got.Load() < want {
		if time.Now().After(deadline) {
			t.Fatalf("lost wakeup: collected %d of %d after %d calls", h.got.Load(), want, h.calls.Load())
		}
		time.Sleep(time.Millisecond)
	}
}

// Completions from many goroutines in waves of mixed sizes, landing while
// the host drains: every one is collected, and calls never overlap.
func TestNotifyNoLostWakeup(t *testing.T) {
	for _, lag := range []time.Duration{0, 200 * time.Microsecond} {
		h := &host{lag: lag}
		install(t, h)
		rng := rand.New(rand.NewSource(1))
		var total int64
		for wave := 0; wave < 400; wave++ {
			n := []int{1, 2, 7, 64, 500, 5000}[rng.Intn(6)]
			var wg sync.WaitGroup
			for i := 0; i < n; i++ {
				wg.Add(1)
				go func() { defer wg.Done(); finish() }()
			}
			wg.Wait()
			total += int64(n)
			if rng.Intn(4) == 0 {
				waitFor(t, h, total) // sometimes go idle between waves
			}
		}
		waitFor(t, h, total)
		if h.overlap.Load() {
			t.Fatal("two calls ran at once")
		}
		// Coalescing: far fewer calls than completions.
		t.Logf("lag %v: %d completions, %d calls", lag, total, h.calls.Load())
		setNotifyFn(nil)
		time.Sleep(20 * time.Millisecond)
	}
}

// The case clearing the flag before calling exists for: a request that
// finishes after the host's last poll but before its call returns still
// gets a call. Clearing after the call would strand it until some later
// request finishes.
func TestNotifyCompletionDuringDrain(t *testing.T) {
	drained := make(chan struct{})
	release := make(chan struct{})
	var armed atomic.Bool
	h := &host{}
	h.afterDrain = func() {
		if armed.CompareAndSwap(true, false) {
			drained <- struct{}{}
			<-release
		}
	}
	install(t, h)
	time.Sleep(10 * time.Millisecond) // the call setNotify schedules is over

	armed.Store(true)
	finish()
	select { // the host has polled the queue empty and not returned yet
	case <-drained:
	case <-time.After(5 * time.Second):
		t.Fatal("no call for the first completion")
	}
	finish()
	close(release)
	waitFor(t, h, 2)
}

// A call with nothing to collect is skipped: neither setNotify nor a
// completion the last drain already took makes one.
func TestNotifySkipsEmptyCalls(t *testing.T) {
	h := &host{}
	install(t, h)
	time.Sleep(10 * time.Millisecond)
	if n := h.calls.Load(); n != 0 {
		t.Fatalf("setNotify with nothing queued made %d calls", n)
	}
	for round := 0; round < 200; round++ {
		before := h.calls.Load()
		var wg sync.WaitGroup
		for i := 0; i < 50; i++ {
			wg.Add(1)
			go func() { defer wg.Done(); finish() }()
		}
		wg.Wait()
		waitFor(t, h, int64(round+1)*50)
		time.Sleep(time.Millisecond)
		if h.calls.Load() == before {
			t.Fatal("no call for a round of completions")
		}
	}
	idle := h.calls.Load()
	time.Sleep(20 * time.Millisecond)
	if h.calls.Load() != idle {
		t.Fatal("calls kept coming with nothing queued")
	}
	// Every call found something to take: the last drain never leaves a
	// trailing empty call behind.
	if h.calls.Load() > h.got.Load() {
		t.Fatalf("%d calls for %d completions", h.calls.Load(), h.got.Load())
	}
}

// With calls off, completions wait in the channel; turning calls on
// schedules one that collects them.
func TestNotifyOffThenOn(t *testing.T) {
	h := &host{}
	notifyCall = h.call
	setNotifyFn(nil)
	for i := 0; i < 10; i++ {
		finish()
	}
	time.Sleep(20 * time.Millisecond)
	if h.got.Load() != 0 {
		t.Fatalf("collected %d while calls were off", h.got.Load())
	}
	install(t, h)
	waitFor(t, h, 10)
}
