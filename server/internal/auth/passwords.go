package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// cost is bcrypt's work factor: about a quarter of a second per hash or check
// on a server core, which is what makes a stolen hash slow to guess. Only
// SetCostForTests changes it, for one test.
var cost = 12

// bcryptSlots bounds the bcrypt work running at once, across the process.
// The container is a quarter of a CPU: without a bound, a handful of
// addresses sending logins within the limiter's allowance (each a fresh
// email, each a full bcrypt) would keep it busy and starve every other
// request, with the waiting logins queuing without end. With it, a login that
// can't get a slot within bcryptWait (or before its request gives up) is
// refused without hashing anything.
var bcryptSlots = make(chan struct{}, 2)

// bcryptWait is how long HashPassword and CheckPassword wait for a slot.
var bcryptWait = 2 * time.Second

// ErrBusy is no bcrypt slot coming free in time: the routes answer it like
// the limiter, 429 "Too many attempts".
var ErrBusy = errors.New("auth: too many password checks at once")

// compareHash and generateHash are bcrypt's, as variables so a test can
// count them.
var (
	compareHash  = bcrypt.CompareHashAndPassword
	generateHash = bcrypt.GenerateFromPassword
)

// AcquireBcryptSlot takes one of the bcrypt slots, as HashPassword and
// CheckPassword do, waiting up to 2 s or until ctx is done (ErrBusy). Call
// release when the work is done; calling it again does nothing.
func AcquireBcryptSlot(ctx context.Context) (release func(), err error) {
	select {
	case bcryptSlots <- struct{}{}:
	default:
		timer := time.NewTimer(bcryptWait)
		defer timer.Stop()
		select {
		case bcryptSlots <- struct{}{}:
		case <-timer.C:
			return nil, ErrBusy
		case <-ctx.Done():
			return nil, ErrBusy
		}
	}
	var once sync.Once
	return func() { once.Do(func() { <-bcryptSlots }) }, nil
}

// A password is 8 to 72 bytes. bcrypt reads only the first 72 and ignores
// the rest without a word, so a longer one is refused rather than cut short.
const (
	minPassword = 8
	maxPassword = 72
)

// ErrPasswordLength is a password HashPassword won't hash; its text is what
// the routes answer.
var ErrPasswordLength = errors.New("A password needs 8 to 72 characters")

// dummyHash is a bcrypt hash (at cost) of a random secret nobody kept. A login
// for an email with no account checks the password against it, so it takes as
// long as one with an account: the time it takes doesn't say who has one.
var dummyHash = productionDummyHash

const productionDummyHash = "$2a$12$pIsZNI/12T6JDFpffK/li.6kBhRMZ7wV13uRo/gzuuTXmegeJsMLO"

// SetCostForTests makes bcrypt run at cost c for the rest of test t, hashes
// and the dummy hash alike, so a test that signs in again and again doesn't
// spend a quarter of a second on each; t.Cleanup puts back cost 12. Tests
// that use it must not run in parallel. (t is a *testing.T; the interface
// keeps package testing out of the production binary.)
func SetCostForTests(t interface {
	Helper()
	Cleanup(func())
	Fatal(args ...any)
}, c int) {
	t.Helper()
	h, err := bcrypt.GenerateFromPassword([]byte("a secret nobody kept"), c)
	if err != nil {
		t.Fatal(err) // c is out of bcrypt's range
	}
	oldCost, oldDummy := cost, dummyHash
	cost, dummyHash = c, string(h)
	t.Cleanup(func() { cost, dummyHash = oldCost, oldDummy })
}

// ValidPassword is whether pw is 8 to 72 bytes long.
func ValidPassword(pw string) bool {
	return len(pw) >= minPassword && len(pw) <= maxPassword
}

// HashPassword is pw's bcrypt hash at cost 12; ErrPasswordLength for a
// password it won't hash, ErrBusy when no bcrypt slot came free.
func HashPassword(ctx context.Context, pw string) (string, error) {
	if !ValidPassword(pw) {
		return "", ErrPasswordLength
	}
	release, err := AcquireBcryptSlot(ctx)
	if err != nil {
		return "", err
	}
	defer release()
	h, err := generateHash([]byte(pw), cost)
	if err != nil {
		return "", err
	}
	return string(h), nil
}

// CheckPassword is whether pw is the password hash was made from. An empty
// hash (no such account) still costs one bcrypt check, against dummyHash, and
// fails; so does a password past 72 bytes, which no stored hash can match.
// err is ErrBusy when no bcrypt slot came free: nothing was checked.
func CheckPassword(ctx context.Context, hash, pw string) (bool, error) {
	release, err := AcquireBcryptSlot(ctx)
	if err != nil {
		return false, err
	}
	defer release()
	known := hash != ""
	if !known {
		hash = dummyHash
	}
	tooLong := len(pw) > maxPassword
	if tooLong {
		pw = pw[:maxPassword] // still spend the time, then refuse
	}
	ok := compareHash([]byte(hash), []byte(pw)) == nil
	return ok && known && !tooLong, nil
}

// NewSecret is a new random secret: 32 bytes from crypto/rand, base64url
// without padding, after prefix ("" for a session cookie, "crm_" for an API
// token); and hash, what the database keeps of it (HashSecret).
func NewSecret(prefix string) (token, hash string) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err) // crypto/rand never fails on the platforms we run on
	}
	token = prefix + base64.RawURLEncoding.EncodeToString(b[:])
	return token, HashSecret(token)
}

// HashSecret is the hex SHA-256 of a whole secret, prefix included. A secret
// carries 256 random bits, so a fast hash is enough: nobody can guess one
// from its hash, and the lookup by hash is an exact match on the database.
func HashSecret(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
