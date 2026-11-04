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
// sitting in waitNext covers every request in flight. Go never calls into the
// host, so nothing here depends on the host's FFI.

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

	*ticket = C.ulonglong(c.ticket)

	return c.out
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
