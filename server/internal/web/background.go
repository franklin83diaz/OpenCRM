package web

import (
	"context"
	"log"
	"net/http"
	"sync"
	"time"
)

var background sync.WaitGroup

// Background is c.executionCtx.waitUntil: fn runs after the response may have
// gone out, on a context that keeps the request's values but is not canceled
// when the request ends. A panic in fn is logged, never fatal — a rejected
// waitUntil promise didn't take the Worker down either.
func Background(r *http.Request, fn func(ctx context.Context)) {
	ctx := context.WithoutCancel(r.Context())
	background.Add(1)
	go func() {
		defer background.Done()
		defer func() {
			if p := recover(); p != nil {
				log.Printf("background work for %s %s: %v", r.Method, r.URL.Path, p)
			}
		}()
		fn(ctx)
	}()
}

// Drain waits up to timeout for background work to finish, reporting whether
// it all did. Call it after the server has stopped taking requests, so nothing
// new starts while it waits.
func Drain(timeout time.Duration) bool {
	done := make(chan struct{})
	go func() {
		background.Wait()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-time.After(timeout):
		return false
	}
}
