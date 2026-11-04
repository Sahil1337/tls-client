package main

/*
// Go can't call a C function pointer itself, so the notifier goes through
// this. It lives here, not in async.go: a file with //export may only
// declare in its preamble, never define.
static void callNotify(void *fn) { ((void (*)(void))fn)(); }
*/
import "C"

import (
	"sync"
	"sync/atomic"
	"unsafe"
)

// Push notifications for the completion queue, for hosts that can take a
// call from a foreign thread: instead of parking a thread in waitNext, the
// host registers a function with setNotify and, each time it is called,
// collects everything that has finished with waitNext(0).
//
// One notifier goroutine makes every call, so the host never sees two at
// once and a burst of completions costs one call, not one per request. The
// host's function may block until the host has handled it (koffi does);
// completions keep queuing meanwhile.

var (
	// The host's function (a C pointer, never Go memory); nil while off.
	notifyFn atomic.Pointer[byte]
	// Set while a call is due and the notifier has not started it yet.
	notifyReady atomic.Bool
	// Completions sent but not yet taken, give or take ones in transit
	// (see notifier). Atomic, not len(completions): the proof below needs
	// it ordered with notifyReady.
	queued atomic.Int64
	// Wakes the notifier. Only the completion that flips notifyReady sends,
	// and the notifier receives before it clears the flag, so at most one
	// token is ever in flight and a send never blocks.
	notifyWake = make(chan struct{}, 1)
	notifyOnce sync.Once
	// How the notifier calls the host; tests swap in a Go func.
	notifyCall = func(fn unsafe.Pointer) { C.callNotify(fn) }
)

// setNotifyFn installs fn (nil turns calls off). Installing one also
// schedules a call: a completion that landed while calls were off would
// otherwise wait for the next one to finish.
func setNotifyFn(fn unsafe.Pointer) {
	notifyFn.Store((*byte)(fn))
	if fn == nil {
		return
	}
	// Hosts that never register a function never pay for the goroutine.
	notifyOnce.Do(func() { go notifier() })
	notifyRaise()
}

// notifyRaise schedules a call unless one is already due. Every completion
// calls it after it is queued (and counted), so it is cheap: one failed CAS
// while a call is pending.
func notifyRaise() {
	if notifyReady.CompareAndSwap(false, true) {
		notifyWake <- struct{}{}
	}
}

func notifier() {
	for range notifyWake {
		// Clear BEFORE calling: a request that finishes while the host is
		// draining finds the flag down and schedules another call, so none
		// is missed. A request that finds it still up was counted before
		// this clear, so the check below sees it and the drain collects it.
		notifyReady.Store(false)
		// Skip calls with nothing to collect: one raised by a request the
		// last drain already took, or by setNotify. Besides saving a trip,
		// koffi aborts the process when a call is still on its way to JS
		// as Node exits (Relay → Napi::Error::Fatal during
		// FreeEnvironment), and the host only keeps Node alive while it
		// waits for responses. Undercounting is safe: a request not counted
		// yet raises again after counting.
		if queued.Load() <= 0 {
			continue
		}
		if fn := notifyFn.Load(); fn != nil {
			notifyCall(unsafe.Pointer(fn))
		}
	}
}
