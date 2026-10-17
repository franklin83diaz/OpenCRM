package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"

	"golang.org/x/crypto/bcrypt"
)

// cost is bcrypt's work factor: about a quarter of a second per hash or check
// on a server core, which is what makes a stolen hash slow to guess.
const cost = 12

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
const dummyHash = "$2a$12$pIsZNI/12T6JDFpffK/li.6kBhRMZ7wV13uRo/gzuuTXmegeJsMLO"

// ValidPassword is whether pw is 8 to 72 bytes long.
func ValidPassword(pw string) bool {
	return len(pw) >= minPassword && len(pw) <= maxPassword
}

// HashPassword is pw's bcrypt hash at cost 12, or ErrPasswordLength.
func HashPassword(pw string) (string, error) {
	if !ValidPassword(pw) {
		return "", ErrPasswordLength
	}
	h, err := bcrypt.GenerateFromPassword([]byte(pw), cost)
	if err != nil {
		return "", err
	}
	return string(h), nil
}

// CheckPassword is whether pw is the password hash was made from. An empty
// hash (no such account) still costs one bcrypt check, against dummyHash, and
// fails; so does a password past 72 bytes, which no stored hash can match.
func CheckPassword(hash, pw string) bool {
	known := hash != ""
	if !known {
		hash = dummyHash
	}
	tooLong := len(pw) > maxPassword
	if tooLong {
		pw = pw[:maxPassword] // still spend the time, then refuse
	}
	ok := bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw)) == nil
	return ok && known && !tooLong
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
