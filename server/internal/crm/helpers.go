package crm

// Helpers the route files share: TS's try/catch around a route body, the
// shapes of its error and ok answers, and the JS built-ins the ported code
// leans on.

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strings"
	"unicode"

	"opencrm/internal/js"
	"opencrm/internal/web"
)

// errorBody is {error: message}, the answer of a failed route.
type errorBody struct {
	Error string `json:"error"`
}

type okBody struct {
	OK bool `json:"ok"`
}

// tryJSON runs a route body inside TS's try/catch: an error answers
// 500 {error: err.message}; otherwise status and body.
func tryJSON(w http.ResponseWriter, fn func() (int, any, error)) error {
	status, body, err := fn()
	if err != nil {
		return web.JSON(w, http.StatusInternalServerError, errorBody{errMessage(err)})
	}
	return web.JSON(w, status, body)
}

func fail(status int, msg string) (int, any, error) { return status, errorBody{msg}, nil }

// trimmedField is `(body.<key> || "").trim()`: a truthy value that is not a
// string has no trim, which TS threw as this TypeError.
func trimmedField(body map[string]any, key string) (string, error) {
	v := body[key]
	if !js.Truthy(v) {
		return "", nil
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf(`(body.%s || "").trim is not a function`, key)
	}
	return js.Trim(s), nil
}

// readNull is the TypeError of reading a property of a null body.
func readNull(key string) error {
	return fmt.Errorf("Cannot read properties of null (reading '%s')", key)
}

// errMessage is err.message as TS stored or returned it. A failed request
// reads as its cause, without Go's `Post "url": ` prefix: the Workers runtime
// worded network failures its own way, and the URL is the internal bridge's.
func errMessage(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err.Error()
	}
	return err.Error()
}

// mayWrite is whether a caller may change tasks: a signed-in person or an
// API token. (TS also let Clawnify's "agent" caller write; there is no such
// caller now. Behind the auth middleware nobody else gets this far.)
func mayWrite(r *http.Request) bool {
	who := web.Caller(r)
	return who == "user" || who == "api"
}

// headerValue is c.req.header(name): every value of the header, comma-joined.
func headerValue(r *http.Request, name string) string {
	return strings.Join(r.Header.Values(name), ", ")
}

func indexOf(list []string, s string) int {
	for i, x := range list {
		if x == s {
			return i
		}
	}
	return -1
}

func flag(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

// jsonNumber is a number as JSON.stringify writes it: NaN and ±Infinity are null.
func jsonNumber(f float64) any {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return nil
	}
	return f
}

// localeCompare approximates String.prototype.localeCompare for the en
// locale (ICU's root collation): punctuation before digits before letters,
// letters by their base letter first, accents next, and lower case before
// upper case last. Enough to order the names and dates the routes sort as TS did.
func localeCompare(a, b string) int {
	ka, kb := collationKey(a), collationKey(b)
	for level := 0; level < 3; level++ {
		x, y := ka[level], kb[level]
		for i := 0; i < len(x) && i < len(y); i++ {
			if x[i] != y[i] {
				if x[i] < y[i] {
					return -1
				}
				return 1
			}
		}
		if len(x) != len(y) {
			if len(x) < len(y) {
				return -1
			}
			return 1
		}
	}
	return 0
}

// collationOrder is ICU's order of printable ASCII (case aside).
const collationOrder = " _-,;:!?.'\"()[]{}@*/\\&#%`^+<=>|~$0123456789abcdefghijklmnopqrstuvwxyz"

// accented maps common accented Latin letters to their base letter.
var accented = func() map[rune]rune {
	m := map[rune]rune{}
	for base, list := range map[rune]string{
		'a': "àáâãäåāăą", 'c': "çćĉċč", 'd': "ď", 'e': "èéêëēĕėęě", 'g': "ĝğġģ", 'h': "ĥ",
		'i': "ìíîïĩīĭįı", 'j': "ĵ", 'k': "ķ", 'l': "ĺļľ", 'n': "ñńņňŉ", 'o': "òóôõöøōŏő",
		'r': "ŕŗř", 's': "śŝşšß", 't': "ţť", 'u': "ùúûüũūŭůűų", 'w': "ŵ", 'y': "ýÿŷ", 'z': "źżž",
	} {
		for _, r := range list {
			m[r] = base
			m[unicode.ToUpper(r)] = base
		}
	}
	return m
}()

// collationKey is a string's primary (letter), secondary (accent) and
// tertiary (case) weights.
func collationKey(s string) [3][]int {
	var k [3][]int
	for _, r := range s {
		lower := unicode.ToLower(r)
		accent := 0
		if base, ok := accented[r]; ok {
			accent, lower = int(unicode.ToLower(r)), base
		}
		primary := 1000 + int(lower) // anything else sorts after the letters, by code point
		if lower < 128 {
			if i := strings.IndexRune(collationOrder, lower); i >= 0 {
				primary = i
			}
		}
		tertiary := 0
		if unicode.IsUpper(r) {
			tertiary = 1
		}
		k[0] = append(k[0], primary)
		k[1] = append(k[1], accent)
		k[2] = append(k[2], tertiary)
	}
	return k
}
