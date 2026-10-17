package crm

// Email rules that outlive the Gmail sync: an address's domain and the
// blocklist the Customers page still reads. No I/O here.

import (
	"regexp"
	"strings"

	"opencrm/internal/js"
)

// wsClass is JavaScript's \s: Go's \s lacks \v and the Unicode spaces.
const wsClass = `[\t\n\v\f\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}]`

// dotClass is JavaScript's `.`: any character but a line terminator.
const dotClass = `[^\n\r\x{2028}\x{2029}]`

var (
	leadingAt = regexp.MustCompile(`^@`)
)

// DomainOf is the lower-cased part after the last @, or "".
func DomainOf(email string) string {
	at := strings.LastIndex(email, "@")
	if at < 0 {
		return ""
	}
	return strings.ToLower(email[at+1:])
}

// NormaliseBlocklist reads blocklist entries, which are addresses
// ("ada@x.com") or domains ("@x.com" or "x.com"). Normalised to lower case,
// blanks dropped.
func NormaliseBlocklist(entries any) []string {
	list, ok := entries.([]any)
	out := []string{}
	if !ok {
		return out
	}
	seen := map[string]bool{}
	for _, e := range list {
		s, ok := e.(string)
		if !ok {
			continue
		}
		v := strings.ToLower(js.Trim(s))
		if v == "" {
			continue
		}
		if !(strings.Contains(v, "@") && !strings.HasPrefix(v, "@")) {
			v = "@" + leadingAt.ReplaceAllString(v, "")
		}
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

// IsBlocked is whether the address, or its whole domain, is on the blocklist.
func IsBlocked(email string, blocklist []string) bool {
	domain := "@" + DomainOf(email)
	for _, b := range blocklist {
		if b == email || b == domain {
			return true
		}
	}
	return false
}
