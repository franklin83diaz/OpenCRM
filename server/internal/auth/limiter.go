package auth

import (
	"net/netip"
	"sync"
	"time"
)

// Limiter slows down password guessing: at most 10 login attempts a minute
// from one address, and 10 attempts in 15 minutes at one email that didn't end
// in a successful sign-in (an attempt that never checked a password is taken
// back: Unchecked); and, apart from those, 10 tries in 15 minutes at an
// account's current password when changing it. It lives in memory, which is
// right because the API runs as a single container instance (max_instances =
// 1); a restart (the container sleeping after 10 idle minutes, or a deploy)
// starts it afresh.
type Limiter struct {
	mu        sync.Mutex
	now       func() time.Time
	ips       window
	emails    window
	changes   window
	lastSweep time.Time
	attempts  uint64 // numbers each counted attempt, for Unchecked
}

const (
	ipAttempts    = 10
	ipPeriod      = time.Minute
	emailFailures = 10
	emailPeriod   = 15 * time.Minute
	maxKey        = 320 // longer keys are cut: an email is at most 254 bytes, an address far less
)

// NewLimiter is an empty limiter on the given clock (nil: time.Now).
func NewLimiter(now func() time.Time) *Limiter {
	if now == nil {
		now = time.Now
	}
	return &Limiter{
		now:    now,
		ips:    window{limit: ipAttempts, period: ipPeriod, hits: map[string][]hit{}},
		emails: window{limit: emailFailures, period: emailPeriod, hits: map[string][]hit{}},
		// The same allowance as an email's, but its own: someone locking an
		// email out of signing in can't stop its owner, signed in, from
		// changing the password.
		changes: window{limit: emailFailures, period: emailPeriod, hits: map[string][]hit{}},
	}
}

// Attempt is a sign-in attempt Allow let through, for Unchecked.
type Attempt struct {
	ip, email string
	n         uint64
}

// Allow is whether an attempt to sign in as email from ip may go ahead, and
// that attempt. An allowed attempt counts against ip right away, and against
// email until Succeeded clears it: counting it before the password is checked
// means a burst of parallel guesses can't all slip in before the first one
// fails. A refused attempt counts against nothing.
func (l *Limiter) Allow(ip, email string) (Attempt, bool) {
	ip, email = client(ip), cut(email)
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.sweep(now)
	if l.ips.full(ip, now) || l.emails.full(email, now) {
		return Attempt{}, false
	}
	l.attempts++
	l.ips.add(ip, now, l.attempts)
	l.emails.add(email, now, l.attempts)
	return Attempt{ip: ip, email: email, n: l.attempts}, true
}

// Unchecked takes back an attempt that ended without checking a password (no
// bcrypt slot came free, the CRM has no accounts yet): only a guess counts,
// against its address or its email. It takes back that attempt alone, if it
// still counts; calling it again does nothing.
func (l *Limiter) Unchecked(a Attempt) {
	if a.n == 0 {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.ips.remove(a.ip, a.n)
	l.emails.remove(a.email, a.n)
}

// Succeeded forgets email's attempts: its owner signed in.
func (l *Limiter) Succeeded(email string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.emails.hits, cut(email))
}

// AllowPasswordChange is whether the account userID may try its current
// password again to change it; the try counts until PasswordChanged.
func (l *Limiter) AllowPasswordChange(userID string) bool {
	userID = cut(userID)
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.sweep(now)
	if l.changes.full(userID, now) {
		return false
	}
	l.changes.add(userID, now, 0)
	return true
}

// PasswordChanged forgets userID's tries: the password was right.
func (l *Limiter) PasswordChanged(userID string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.changes.hits, cut(userID))
}

// sweep drops keys whose attempts have all left their window, at most once a
// minute, so the maps hold only what the last 15 minutes saw.
func (l *Limiter) sweep(now time.Time) {
	if now.Sub(l.lastSweep) < time.Minute {
		return
	}
	l.lastSweep = now
	l.ips.sweep(now)
	l.emails.sweep(now)
	l.changes.sweep(now)
}

// client is the key an address counts under. An IPv6 client is usually given
// a whole /64, and can pick any address in it: it counts as one. Anything
// that isn't an address counts under one shared key, so a made-up header
// can't open a fresh count for each request.
func client(ip string) string {
	a, err := netip.ParseAddr(ip)
	if err != nil {
		return "unknown"
	}
	a = a.Unmap()
	if a.Is4() {
		return a.String()
	}
	p, err := a.WithZone("").Prefix(64)
	if err != nil {
		return "unknown"
	}
	return p.String()
}

func cut(key string) string {
	if len(key) > maxKey {
		return key[:maxKey]
	}
	return key
}

// window counts hits per key over a sliding period; a key keeps at most limit
// of them, the newest.
type window struct {
	limit  int
	period time.Duration
	hits   map[string][]hit
}

// hit is one counted attempt: when, and its number (Limiter.attempts; 0 for
// a password-change try, which is never taken back).
type hit struct {
	at time.Time
	n  uint64
}

// recent is key's hits within the period, oldest first, and stores them back.
func (w *window) recent(key string, now time.Time) []hit {
	hits := w.hits[key]
	i := 0
	for i < len(hits) && now.Sub(hits[i].at) >= w.period {
		i++
	}
	if i > 0 {
		hits = append([]hit(nil), hits[i:]...)
		if len(hits) == 0 {
			delete(w.hits, key)
		} else {
			w.hits[key] = hits
		}
	}
	return hits
}

func (w *window) full(key string, now time.Time) bool {
	return len(w.recent(key, now)) >= w.limit
}

func (w *window) add(key string, now time.Time, n uint64) {
	hits := append(w.recent(key, now), hit{now, n})
	if len(hits) > w.limit {
		hits = hits[len(hits)-w.limit:]
	}
	w.hits[key] = hits
}

// remove drops attempt n from key's hits, if it is there.
func (w *window) remove(key string, n uint64) {
	hits := w.hits[key]
	for i, h := range hits {
		if h.n == n {
			hits = append(hits[:i:i], hits[i+1:]...)
			if len(hits) == 0 {
				delete(w.hits, key)
			} else {
				w.hits[key] = hits
			}
			return
		}
	}
}

func (w *window) sweep(now time.Time) {
	for key, hits := range w.hits {
		if len(hits) == 0 || now.Sub(hits[len(hits)-1].at) >= w.period {
			delete(w.hits, key)
		}
	}
}
