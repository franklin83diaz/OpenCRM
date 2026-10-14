// Package js reproduces the few JavaScript semantics the TS backend leaned on —
// Number(), String(), truthiness, Date parsing and toISOString — so ported code
// can say what it means once instead of re-deriving JS's rules at each call site.
package js

import (
	"crypto/rand"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// UUID is crypto.randomUUID(): a random (version 4) UUID in lower case.
func UUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err) // crypto/rand never fails on the platforms we run on
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// ISO is Date.prototype.toISOString: UTC, always with milliseconds.
func ISO(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05.000Z")
}

var datePattern = regexp.MustCompile(
	`^(\d{4})-(\d{2})-(\d{2})(?:[Tt ](\d{2}):(\d{2})(?::(\d{2})(?:\.(\d+))?)?([Zz]|[+-]\d{2}:?\d{2})?)?$`)

// ParseDate is new Date(s) for the strings this app stores and receives: ISO
// dates and datetimes and SQLite's datetime('now') ("2026-10-14 16:10:00").
// A string without a zone is UTC — the Workers runtime ran in UTC, so that is
// what TS got for SQLite's zone-less timestamps, whatever this machine's zone is.
// Like V8, an out-of-range day rolls over (Feb 31 is Mar 3) and 24:00 is the
// next midnight; fractions beyond milliseconds are dropped.
func ParseDate(s string) (time.Time, bool) {
	m := datePattern.FindStringSubmatch(Trim(s))
	if m == nil {
		return time.Time{}, false
	}
	num := func(i int) int {
		n, _ := strconv.Atoi(m[i]) // "" (absent) reads as 0
		return n
	}
	year, month, day := num(1), num(2), num(3)
	hour, minute, sec := num(4), num(5), num(6)
	ms := 0
	if frac := m[7]; frac != "" {
		ms, _ = strconv.Atoi((frac + "00")[:3])
	}
	if month < 1 || month > 12 || day < 1 || day > 31 || minute > 59 || sec > 59 ||
		hour > 24 || (hour == 24 && (minute != 0 || sec != 0 || ms != 0)) {
		return time.Time{}, false
	}
	t := time.Date(year, time.Month(month), day, hour, minute, sec, ms*int(time.Millisecond), time.UTC)
	if zone := m[8]; zone != "" && zone != "Z" && zone != "z" {
		digits := strings.ReplaceAll(zone[1:], ":", "")
		oh, _ := strconv.Atoi(digits[:2])
		om, _ := strconv.Atoi(digits[2:])
		if oh > 23 || om > 59 {
			return time.Time{}, false
		}
		offset := time.Duration(oh)*time.Hour + time.Duration(om)*time.Minute
		if zone[0] == '+' {
			offset = -offset
		}
		t = t.Add(offset)
	}
	return t, true
}

// Trim is String.prototype.trim: JS whitespace and line terminators at both ends.
func Trim(s string) string {
	return strings.TrimFunc(s, isJSSpace)
}

func isJSSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', '\u00a0', '\u2028', '\u2029', '\ufeff':
		return true
	}
	return unicode.Is(unicode.Zs, r)
}

var decimalPattern = regexp.MustCompile(`^[+-]?(?:\d+\.?\d*|\.\d+)(?:[eE][+-]?\d+)?$`)

// Number is JS Number(v) for values that come out of JSON and the database.
func Number(v any) float64 {
	switch v := v.(type) {
	case nil:
		return 0 // Number(null); Number(undefined) is NaN, but Go cannot tell them apart
	case bool:
		if v {
			return 1
		}
		return 0
	case float64:
		return v
	case float32:
		return float64(v)
	case int:
		return float64(v)
	case int64:
		return float64(v)
	case string:
		return numberFromString(v)
	case []any:
		return numberFromString(String(v))
	default:
		return math.NaN()
	}
}

func numberFromString(s string) float64 {
	s = Trim(s)
	switch s {
	case "":
		return 0
	case "Infinity", "+Infinity":
		return math.Inf(1)
	case "-Infinity":
		return math.Inf(-1)
	}
	if decimalPattern.MatchString(s) {
		f, err := strconv.ParseFloat(s, 64)
		if err != nil && !isRangeErr(err) {
			return math.NaN()
		}
		return f // out of range is ±Infinity or 0, as in JS
	}
	if len(s) > 2 && s[0] == '0' {
		var base float64
		switch s[1] {
		case 'x', 'X':
			base = 16
		case 'o', 'O':
			base = 8
		case 'b', 'B':
			base = 2
		default:
			return math.NaN()
		}
		n := 0.0
		for _, c := range strings.ToLower(s[2:]) {
			d := strings.IndexRune("0123456789abcdef", c)
			if d < 0 || float64(d) >= base {
				return math.NaN()
			}
			n = n*base + float64(d)
		}
		return n
	}
	return math.NaN()
}

func isRangeErr(err error) bool {
	ne, ok := err.(*strconv.NumError)
	return ok && ne.Err == strconv.ErrRange
}

// String is JS String(v). nil is JSON null, so it reads "null".
func String(v any) string {
	switch v := v.(type) {
	case nil:
		return "null"
	case string:
		return v
	case bool:
		return strconv.FormatBool(v)
	case float64:
		return formatNumber(v)
	case float32:
		return formatNumber(float64(v))
	case int:
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case []any:
		// Array.prototype.join: null and undefined elements are empty.
		parts := make([]string, len(v))
		for i, e := range v {
			if e != nil {
				parts[i] = String(e)
			}
		}
		return strings.Join(parts, ",")
	case map[string]any:
		return "[object Object]"
	default:
		return fmt.Sprint(v)
	}
}

// formatNumber is Number.prototype.toString(): the shortest round-tripping
// digits, in plain notation for exponents in [-7, 21) and exponent notation
// ("1e+21", "1e-7") outside it.
func formatNumber(f float64) string {
	switch {
	case math.IsNaN(f):
		return "NaN"
	case math.IsInf(f, 1):
		return "Infinity"
	case math.IsInf(f, -1):
		return "-Infinity"
	case f == 0:
		return "0" // and -0
	}
	sign := ""
	if f < 0 {
		sign, f = "-", -f
	}
	// "d.ddde±x" → digits "dddd" and n, where f = 0.dddd × 10^n.
	e := strconv.FormatFloat(f, 'e', -1, 64)
	mant, exp, _ := strings.Cut(e, "e")
	digits := strings.Replace(mant, ".", "", 1)
	x, _ := strconv.Atoi(exp)
	k, n := len(digits), x+1
	switch {
	case k <= n && n <= 21:
		return sign + digits + strings.Repeat("0", n-k)
	case 0 < n && n <= 21:
		return sign + digits[:n] + "." + digits[n:]
	case -6 < n && n <= 0:
		return sign + "0." + strings.Repeat("0", -n) + digits
	}
	expSign := "+"
	if n-1 < 0 {
		expSign = "-"
	}
	frac := ""
	if k > 1 {
		frac = "." + digits[1:]
	}
	return sign + digits[:1] + frac + "e" + expSign + strconv.Itoa(abs(n-1))
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// Truthy is JS truthiness: nil, false, "", 0 and NaN are false; every other
// value — including empty arrays and objects — is true.
func Truthy(v any) bool {
	switch v := v.(type) {
	case nil:
		return false
	case bool:
		return v
	case string:
		return v != ""
	case float64:
		return v != 0 && !math.IsNaN(v)
	case float32:
		return v != 0 && !math.IsNaN(float64(v))
	case int:
		return v != 0
	case int64:
		return v != 0
	default:
		return true
	}
}
