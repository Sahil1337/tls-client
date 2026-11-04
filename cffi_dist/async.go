package main

/*
#include <stdlib.h>
*/
import "C"

import (
	"fmt"
	"sync/atomic"
	"time"
	"unsafe"

	tls_client_cffi_src "github.com/bogdanfinn/tls-client/cffi_src"
)

// Completion queue for hosts that can't afford one blocked thread per request.
// requestAsync runs a request on a goroutine and returns a ticket right away;
// waitNext hands back finished requests in completion order. One host thread
// sitting in waitNext covers every request in flight. Hosts that can take a
// call from a foreign thread can skip the waiting thread: see setNotify.

type completion struct {
	ticket uint64
	out    *C.char
}

var (
	nextTicket atomic.Uint64
	// Finished requests waiting for the host. Senders block if it is ever full.
	completions = make(chan completion, 4096)
)

//export requestAsync
func requestAsync(requestParams *C.char) C.ulonglong {
	// Copy now, the caller may free requestParams as soon as we return.
	requestParamsJson := C.GoString(requestParams)
	ticket := nextTicket.Add(1)

	go func() {
		completions <- completion{ticket: ticket, out: runRequest(requestParamsJson)}
		// After the send, so the call it schedules finds this one queued.
		queued.Add(1)
		notifyRaise()
	}()

	return C.ulonglong(ticket)
}

// waitNext returns the next finished response (the same JSON as request(),
// freed with freeMemory) and writes its ticket to *ticket. timeoutMs 0 polls
// without blocking, a negative timeoutMs waits forever. NULL means nothing
// finished in time.
//
//export waitNext
func waitNext(timeoutMs C.int, ticket *C.ulonglong) *C.char {
	var c completion

	switch {
	case timeoutMs == 0:
		select {
		case c = <-completions:
		default:
			return nil
		}
	case timeoutMs < 0:
		c = <-completions
	default:
		timer := time.NewTimer(time.Duration(timeoutMs) * time.Millisecond)
		defer timer.Stop()

		select {
		case c = <-completions:
		case <-timer.C:
			return nil
		}
	}

	queued.Add(-1)
	*ticket = C.ulonglong(c.ticket)

	return c.out
}

// setNotify registers fn, a `void (*)(void)`, to be called (from a Go thread,
// never two at once) whenever requests have finished; the host then drains
// them with waitNext(0). One call can cover many requests, and a request
// that finishes during a drain gets another call. NULL turns calls off. fn
// must stay callable even after it is replaced: a call may already be under
// way when setNotify returns. There is one fn and one queue per process, so
// a second host in it (another worker_thread) takes over the first's
// completions.
//
//export setNotify
func setNotify(fn unsafe.Pointer) {
	setNotifyFn(fn)
}

// runRequest takes the same path as the blocking request() export. A panic on
// this goroutine would take down the host process, so it is turned into an
// error response like any other failure.
func runRequest(requestParamsJson string) (out *C.char) {
	defer func() {
		if r := recover(); r != nil {
			clientErr := tls_client_cffi_src.NewTLSClientError(fmt.Errorf("request panicked: %v", r))

			out = handleErrorResponse("", false, clientErr)
		}
	}()

	cRequestParams := C.CString(requestParamsJson)
	defer C.free(unsafe.Pointer(cRequestParams))

	return request(cRequestParams)
}
