package web

import (
	"context"
	"log"
	"net/http"
	"sync"
	"time"
)

// background counts running work. Not a sync.WaitGroup: a request that is
// still running when shutdown gives up on it may start work while Drain is
// waiting, and a WaitGroup forbids that Add.
var background struct {
	sync.Mutex
	running int
	idle    chan struct{} // closed when running drops to 0
}

// Background is c.executionCtx.waitUntil: fn runs after the response may have
// gone out, on a context that keeps the request's values but is not canceled
// when the request ends. A panic in fn is logged, never fatal — a rejected
// waitUntil promise didn't take the Worker down either.
func Background(r *http.Request, fn func(ctx context.Context)) {
	ctx := context.WithoutCancel(r.Context())
	background.Lock()
	if background.running == 0 {
		background.idle = make(chan struct{})
	}
	background.running++
	background.Unlock()
	go func() {
		defer func() {
			background.Lock()
			background.running--
			if background.running == 0 {
				close(background.idle)
			}
			background.Unlock()
		}()
		defer func() {
			if p := recover(); p != nil {
				log.Printf("background work for %s %s: %v", r.Method, r.URL.Path, p)
			}
		}()
		fn(ctx)
	}()
}

// Drain waits up to timeout for background work to finish — including work
// started while it waits — and reports whether it all did.
func Drain(timeout time.Duration) bool {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		background.Lock()
		if background.running == 0 {
			background.Unlock()
			return true
		}
		idle := background.idle
		background.Unlock()
		select {
		case <-idle: // look again: more work may have started since
		case <-deadline.C:
			return false
		}
	}
}
