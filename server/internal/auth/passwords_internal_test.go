package auth

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// With every bcrypt slot taken, HashPassword and CheckPassword wait a while
// (or until the request gives up) and then refuse, without running bcrypt:
// a flood of logins can't queue unbounded CPU work.
func TestBcryptSlots(t *testing.T) {
	var runs atomic.Int32
	compare, generate, wait := compareHash, generateHash, bcryptWait
	t.Cleanup(func() { compareHash, generateHash, bcryptWait = compare, generate, wait })
	compareHash = func(h, p []byte) error { runs.Add(1); return compare(h, p) }
	generateHash = func(p []byte, c int) ([]byte, error) { runs.Add(1); return generate(p, c) }

	if cap(bcryptSlots) != 2 || wait != 2*time.Second {
		t.Errorf("%d slots, wait %v; want 2 slots, 2s", cap(bcryptSlots), wait)
	}
	bcryptWait = 50 * time.Millisecond
	ctx := context.Background()
	var releases []func()
	for i := 0; i < cap(bcryptSlots); i++ {
		release, err := AcquireBcryptSlot(ctx)
		if err != nil {
			t.Fatal(err)
		}
		releases = append(releases, release)
	}

	start := time.Now()
	if ok, err := CheckPassword(ctx, dummyHash, "password-1"); ok || !errors.Is(err, ErrBusy) {
		t.Errorf("CheckPassword with no slot = %v, %v", ok, err)
	}
	if _, err := HashPassword(ctx, "password-1"); !errors.Is(err, ErrBusy) {
		t.Errorf("HashPassword with no slot: %v", err)
	}
	if d := time.Since(start); d < 100*time.Millisecond || d > 2*time.Second {
		t.Errorf("waited %v for two 50ms waits", d)
	}

	// A request that gives up stops waiting at once.
	bcryptWait = time.Hour
	gone, cancel := context.WithCancel(ctx)
	cancel()
	start = time.Now()
	if _, err := CheckPassword(gone, dummyHash, "password-1"); !errors.Is(err, ErrBusy) {
		t.Errorf("CheckPassword, request gone: %v", err)
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("a cancelled request waited %v", d)
	}
	if n := runs.Load(); n != 0 {
		t.Fatalf("bcrypt ran %d times without a slot", n)
	}

	// Freed (twice over: release is idempotent), the slots work again.
	for _, release := range releases {
		release()
		release()
	}
	bcryptWait = 50 * time.Millisecond
	h, err := HashPassword(ctx, "password-1")
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := CheckPassword(ctx, h, "password-1"); !ok || err != nil {
		t.Errorf("CheckPassword after release = %v, %v", ok, err)
	}
	if n := runs.Load(); n != 2 {
		t.Errorf("bcrypt ran %d times, want 2", n)
	}
	if len(bcryptSlots) != 0 {
		t.Errorf("%d slots still held", len(bcryptSlots))
	}
}
