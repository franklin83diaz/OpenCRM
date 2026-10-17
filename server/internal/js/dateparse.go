package js

import (
	"math"
	"unicode"
	"unicode/utf16"
)

// DateParse is Date.parse(s): milliseconds since the epoch, NaN when V8 reads
// no date in s. It is V8's DateParser (src/date/dateparser*.{h,cc}): ES5 ISO
// date-time strings ("2026-10-14", "2026-10", "+002026-10-14T10:00:00.5Z",
// "…+0200"), and when a string isn't one, the legacy dates browsers accept
// ("October 1, 2026", "2026/10/01", "10/1/2026 3:04 PM EST", "Thu Oct 01
// 2026 10:00:00 GMT+0200 (CEST)"). Local time is UTC, as on Workers.
//
// ParseDate stays the reader for the dates this app stores; DateParse is for
// text a caller sent that TS handed to Date.parse.
func DateParse(s string) float64 {
	p := dateParser{in: dateInput{s: utf16.Encode([]rune(s))}}
	p.in.next()
	p.tok = p.scan()
	out, ok := p.parse()
	if !ok {
		return math.NaN()
	}
	date := makeDay(out[0], out[1], out[2])*msPerDay + ((out[3]*60+out[4])*60+out[5])*1000 + out[6]
	if !math.IsNaN(out[7]) {
		date -= out[7] * 1000
	}
	if math.Abs(date) > 8.64e15 { // TimeClip
		return math.NaN()
	}
	return math.Trunc(date) + 0
}

const (
	msPerDay = 86_400_000
	dpNone   = math.MaxInt32 // kNone: a missing value
	dpDigits = 9             // kMaxSignificantDigits
)

// makeDay is ES's MakeDay for a 0-based month (0–11) and a day that may run
// past the month's end (Feb 30 is Mar 2): days since the epoch. Howard
// Hinnant's days-from-civil, over years that start in March.
func makeDay(year, month, day float64) float64 {
	y, m := int64(year), int64(month)
	if m < 2 {
		y--
	}
	era := y / 400
	if y < 0 && y%400 != 0 {
		era--
	}
	yoe := y - era*400
	mp := (m + 10) % 12 // March is 0
	doy := (153*mp + 2) / 5
	doe := yoe*365 + yoe/4 - yoe/100 + doy
	return float64(era*146097+doe-719468) + day - 1
}

// dateInput is V8's InputReader over UTF-16 units: ch is 0 at the end (and
// at a NUL, which ends the string for V8 too).
type dateInput struct {
	s   []uint16
	pos int // index_: one past ch
	ch  uint16
}

func (in *dateInput) next() {
	in.ch = 0
	if in.pos < len(in.s) {
		in.ch = in.s[in.pos]
	}
	in.pos++
}

func (in *dateInput) isDigit() bool { return in.ch >= '0' && in.ch <= '9' }

// isWhiteSpace is V8's IsWhiteSpace: WhiteSpace without line terminators.
func isWhiteSpace(c uint16) bool {
	switch c {
	case '\t', '\v', '\f', ' ', 0xa0, 0xfeff:
		return true
	}
	return c >= 0x80 && unicode.Is(unicode.Zs, rune(c))
}

func isWhiteSpaceOrLineTerminator(c uint16) bool {
	return isWhiteSpace(c) || c == '\n' || c == '\r' || c == 0x2028 || c == 0x2029
}

// readNumeral reads digits as a number, leading zeros skipped and at most
// nine significant digits kept (the rest are read and dropped).
func (in *dateInput) readNumeral() int {
	n, i := 0, 0
	for in.ch == '0' {
		in.next()
	}
	for in.isDigit() {
		if i < dpDigits {
			n = n*10 + int(in.ch-'0')
		}
		i++
		in.next()
	}
	return n
}

// readWord reads a word (units >= 'A' that aren't white space), keeping a
// lower-cased three-unit prefix.
func (in *dateInput) readWord() (prefix [3]uint16, length int) {
	for ; in.ch >= 'A' && !isWhiteSpace(in.ch); in.next() {
		if length < 3 {
			prefix[length] = in.ch | 0x20
		}
		length++
	}
	return prefix, length
}

type dateTag int

const (
	tagInvalid dateTag = iota - 6
	tagUnknown
	tagWhiteSpace
	tagNumber
	tagSymbol
	tagEnd
	// keywords are tags 0 and up: their KeywordType
)

const (
	kwInvalid = iota
	kwMonth
	kwZone
	kwTimeSeparator
	kwAmPm
)

type dateToken struct {
	tag    dateTag
	length int
	value  int
}

func (t dateToken) isNumber() bool                 { return t.tag == tagNumber }
func (t dateToken) isSymbol(c byte) bool           { return t.tag == tagSymbol && t.value == int(c) }
func (t dateToken) isKeyword() bool                { return t.tag >= 0 }
func (t dateToken) isKeywordType(k int) bool       { return t.tag == dateTag(k) }
func (t dateToken) isFixedNumber(n int) bool       { return t.isNumber() && t.length == n }
func (t dateToken) isSign() bool                   { return t.tag == tagSymbol && (t.value == '-' || t.value == '+') }
func (t dateToken) sign() int                      { return 44 - t.value }
func (t dateToken) isKeywordZ() bool               { return t.isKeywordType(kwZone) && t.length == 1 && t.value == 0 }
func between(x, lo, hi int) bool                   { return uint(x-lo) <= uint(hi-lo) }
func isMinute(x int) bool                          { return between(x, 0, 59) }
func isHour(x int) bool                            { return between(x, 0, 23) }
func isMonth(x int) bool                           { return between(x, 1, 12) }
func isDayOfMonth(x int) bool                      { return between(x, 1, 31) }
func newKeyword(kind, value, length int) dateToken { return dateToken{dateTag(kind), length, value} }

// keywords is V8's KeywordTable: month names (any word starting with one),
// am/pm, time zone names, and the "T" of an ISO date-time.
var keywords = []struct {
	prefix string
	kind   int
	value  int
}{
	{"jan", kwMonth, 1}, {"feb", kwMonth, 2}, {"mar", kwMonth, 3}, {"apr", kwMonth, 4},
	{"may", kwMonth, 5}, {"jun", kwMonth, 6}, {"jul", kwMonth, 7}, {"aug", kwMonth, 8},
	{"sep", kwMonth, 9}, {"oct", kwMonth, 10}, {"nov", kwMonth, 11}, {"dec", kwMonth, 12},
	{"am", kwAmPm, 0}, {"pm", kwAmPm, 12},
	{"ut", kwZone, 0}, {"utc", kwZone, 0}, {"z", kwZone, 0}, {"gmt", kwZone, 0},
	{"cdt", kwZone, -5}, {"cst", kwZone, -6}, {"edt", kwZone, -4}, {"est", kwZone, -5},
	{"mdt", kwZone, -6}, {"mst", kwZone, -7}, {"pdt", kwZone, -7}, {"pst", kwZone, -8},
	{"t", kwTimeSeparator, 0},
}

func lookupKeyword(prefix [3]uint16, length int) dateToken {
	for _, k := range keywords {
		var want [3]uint16
		for i := 0; i < len(k.prefix); i++ {
			want[i] = uint16(k.prefix[i])
		}
		// A word longer than the keyword is only a month name.
		if want == prefix && (length <= 3 || k.kind == kwMonth) {
			return newKeyword(k.kind, k.value, length)
		}
	}
	return newKeyword(kwInvalid, 0, length)
}

type dateParser struct {
	in  dateInput
	tok dateToken // the scanner's next token
}

func (p *dateParser) scan() dateToken {
	in := &p.in
	pre := in.pos
	switch {
	case in.ch == 0:
		return dateToken{tagEnd, 0, -1}
	case in.isDigit():
		n := in.readNumeral()
		return dateToken{tagNumber, in.pos - pre, n}
	case in.ch == ':' || in.ch == '-' || in.ch == '+' || in.ch == '.' || in.ch == ')':
		c := in.ch
		in.next()
		return dateToken{tagSymbol, 1, int(c)}
	case in.ch >= 'A' && !isWhiteSpace(in.ch):
		return lookupKeyword(in.readWord())
	case isWhiteSpaceOrLineTerminator(in.ch):
		in.next()
		return dateToken{tagWhiteSpace, in.pos - pre, -1}
	case in.ch == '(':
		for balance := 0; ; {
			if in.ch == ')' {
				balance--
			} else if in.ch == '(' {
				balance++
			}
			in.next()
			if balance <= 0 || in.ch == 0 {
				break
			}
		}
		return dateToken{tagUnknown, 1, -1}
	}
	in.next()
	return dateToken{tagUnknown, 1, -1}
}

func (p *dateParser) take() dateToken {
	t := p.tok
	p.tok = p.scan()
	return t
}

func (p *dateParser) skipSymbol(c byte) bool {
	if p.tok.isSymbol(c) {
		p.tok = p.scan()
		return true
	}
	return false
}

type dayComposer struct {
	comp       [3]int
	n          int
	namedMonth int
	iso        bool
}

func (d *dayComposer) add(x int) bool {
	if d.n < 3 {
		d.comp[d.n] = x
		d.n++
		return true
	}
	return false
}

func (d *dayComposer) write() (year, month, day int, ok bool) {
	if d.n < 1 {
		return 0, 0, 0, false
	}
	for d.n < 3 {
		d.comp[d.n] = 1
		d.n++
	}
	month, day = dpNone, dpNone
	if d.namedMonth == dpNone {
		if d.iso || !isDayOfMonth(d.comp[0]) {
			year, month, day = d.comp[0], d.comp[1], d.comp[2]
		} else {
			month, day, year = d.comp[0], d.comp[1], d.comp[2]
		}
	} else {
		month = d.namedMonth
		switch {
		case d.n == 1: // never: n is 3 by now, as in V8
			day = d.comp[0]
		case !isDayOfMonth(d.comp[0]):
			year, day = d.comp[0], d.comp[1]
		default:
			day, year = d.comp[0], d.comp[1]
		}
	}
	if !d.iso {
		if between(year, 0, 49) {
			year += 2000
		} else if between(year, 50, 99) {
			year += 1900
		}
	}
	return year, month, day, isMonth(month) && isDayOfMonth(day)
}

type timeComposer struct {
	comp       [4]int
	n          int
	hourOffset int
}

func (t *timeComposer) empty() bool { return t.n == 0 }

func (t *timeComposer) expecting(x int) bool {
	return (t.n == 1 && isMinute(x)) || (t.n == 2 && isMinute(x)) || (t.n == 3 && between(x, 0, 999))
}

func (t *timeComposer) add(x int) bool {
	if t.n < 4 {
		t.comp[t.n] = x
		t.n++
		return true
	}
	return false
}

func (t *timeComposer) addFinal(x int) bool {
	if !t.add(x) {
		return false
	}
	for t.n < 4 {
		t.comp[t.n] = 0
		t.n++
	}
	return true
}

func (t *timeComposer) write() (h, m, s, ms int, ok bool) {
	for t.n < 4 {
		t.comp[t.n] = 0
		t.n++
	}
	h, m, s, ms = t.comp[0], t.comp[1], t.comp[2], t.comp[3]
	if t.hourOffset != dpNone {
		if !between(h, 0, 12) {
			return 0, 0, 0, 0, false
		}
		h = h%12 + t.hourOffset
	}
	if !isHour(h) || !isMinute(m) || !isMinute(s) || !between(ms, 0, 999) {
		if h != 24 || m != 0 || s != 0 || ms != 0 {
			return 0, 0, 0, 0, false
		}
	}
	return h, m, s, ms, true
}

type zoneComposer struct{ sign, hour, minute int }

func (z *zoneComposer) set(hours int) {
	z.sign = 1
	if hours < 0 {
		z.sign = -1
	}
	z.hour, z.minute = hours*z.sign, 0
}

func (z *zoneComposer) setSign(sign int) {
	z.sign = 1
	if sign < 0 {
		z.sign = -1
	}
}

func (z *zoneComposer) expecting(x int) bool {
	return z.hour != dpNone && z.minute == dpNone && isMinute(x)
}

func (z *zoneComposer) isUTC() bool { return z.hour == 0 && z.minute == 0 }

// write is the offset in seconds, NaN when none was given (local time).
func (z *zoneComposer) write() (float64, bool) {
	if z.sign == dpNone {
		return math.NaN(), true
	}
	if z.hour == dpNone {
		z.hour = 0
	}
	if z.minute == dpNone {
		z.minute = 0
	}
	total := uint32(z.hour)*3600 + uint32(z.minute)*60 // V8's unsigned arithmetic
	if total > math.MaxInt32 {
		return 0, false
	}
	return float64(int32(total)) * float64(z.sign), true
}

// readMilliseconds is the first three significant digits of a numeral, as
// told by its value and its number of digits.
func readMilliseconds(t dateToken) int {
	n, length := t.value, t.length
	switch {
	case length == 1:
		return n * 100
	case length == 2:
		return n * 10
	case length > 3:
		length = min(length, dpDigits)
		factor := 1
		for ; length > 3; length-- {
			factor *= 10
		}
		return n / factor
	}
	return n
}

// parse is DateParser::Parse: year, 0-based month, day, hour, minute,
// second, millisecond and the UTC offset in seconds (NaN for local time).
func (p *dateParser) parse() ([8]float64, bool) {
	var out [8]float64
	day := dayComposer{namedMonth: dpNone}
	tm := timeComposer{hourOffset: dpNone}
	tz := zoneComposer{dpNone, dpNone, dpNone}

	token := p.parseES5(&day, &tm, &tz)
	if token.tag == tagInvalid {
		return out, false
	}
	hasNumber := day.n > 0
	for ; token.tag != tagEnd; token = p.take() {
		switch {
		case token.isNumber():
			hasNumber = true
			n := token.value
			if p.skipSymbol(':') {
				if p.skipSymbol(':') {
					if !tm.empty() {
						return out, false
					}
					tm.add(n)
					tm.add(0)
				} else {
					if !tm.add(n) {
						return out, false
					}
					if p.tok.isSymbol('.') {
						p.take()
					}
				}
			} else if p.skipSymbol('.') && tm.expecting(n) {
				tm.add(n)
				if !p.tok.isNumber() {
					return out, false
				}
				ms := readMilliseconds(p.take())
				if ms < 0 {
					return out, false
				}
				tm.addFinal(ms)
			} else if tz.expecting(n) {
				tz.minute = n
			} else if tm.expecting(n) {
				tm.addFinal(n)
				// Require end, white space, "Z", "+" or "-" right after the time.
				if peek := p.tok; peek.tag != tagEnd && peek.tag != tagWhiteSpace && !peek.isKeywordZ() && !peek.isSign() {
					return out, false
				}
			} else {
				if !day.add(n) {
					return out, false
				}
				p.skipSymbol('-')
			}
		case token.isKeyword():
			kind, value := int(token.tag), token.value
			switch {
			case kind == kwAmPm && !tm.empty():
				tm.hourOffset = value
			case kind == kwMonth:
				day.namedMonth = value
				p.skipSymbol('-')
			case kind == kwZone && hasNumber:
				tz.set(value)
			default:
				// Garbage words are illegal once a number has been read, and the
				// first number must be apart from them.
				if hasNumber || p.tok.isNumber() {
					return out, false
				}
			}
		case token.isSign() && (tz.isUTC() || !tm.empty()):
			// A UTC offset (only after UTC or a time); its number may be missing.
			tz.setSign(token.sign())
			n, length := 0, 0
			if p.tok.isNumber() {
				next := p.take()
				n, length = next.value, next.length
			}
			hasNumber = true
			switch {
			case p.tok.isSymbol(':'):
				tz.hour, tz.minute = n, dpNone
			case length == 1 || length == 2:
				tz.hour, tz.minute = n, 0
			case length == 3 || length == 4:
				tz.hour, tz.minute = n/100, n%100
			default:
				return out, false
			}
		case (token.isSign() || token.isSymbol(')')) && hasNumber:
			return out, false // an extra sign or ')' after a number
		}
	}

	y, mo, d, ok := day.write()
	if !ok {
		return out, false
	}
	h, mi, s, ms, ok := tm.write()
	if !ok {
		return out, false
	}
	offset, ok := tz.write()
	if !ok {
		return out, false
	}
	return [8]float64{float64(y), float64(mo - 1), float64(d), float64(h), float64(mi), float64(s), float64(ms), offset}, true
}

// parseES5 reads as much of an ES5 date-time string as there is: the token
// the legacy parser goes on from, the end when the whole string was one, or
// an invalid token when it can't be a date at all.
func (p *dateParser) parseES5(day *dayComposer, tm *timeComposer, tz *zoneComposer) dateToken {
	invalid := dateToken{tagInvalid, 0, -1}
	// The date: [('-'|'+')yy]yyyy['-'MM['-'DD]].
	switch {
	case p.tok.isSign():
		signToken := p.take()
		if !p.tok.isFixedNumber(6) {
			return signToken
		}
		sign := signToken.sign()
		year := p.take().value
		if sign < 0 && year == 0 {
			return signToken
		}
		day.add(sign * year)
	case p.tok.isFixedNumber(4):
		day.add(p.take().value)
	default:
		return p.take()
	}
	if p.skipSymbol('-') {
		if !p.tok.isFixedNumber(2) || !isMonth(p.tok.value) {
			return p.take()
		}
		day.add(p.take().value)
		if p.skipSymbol('-') {
			if !p.tok.isFixedNumber(2) || !isDayOfMonth(p.tok.value) {
				return p.take()
			}
			day.add(p.take().value)
		}
	}
	// The time: 'T'HH':'mm[':'ss['.'sss]] and a zone.
	if !p.tok.isKeywordType(kwTimeSeparator) {
		if p.tok.tag != tagEnd {
			return p.take()
		}
	} else {
		p.take()
		if !p.tok.isFixedNumber(2) || !between(p.tok.value, 0, 24) {
			return invalid
		}
		// 24:00[:00[.000]] is allowed, no other time starting with 24.
		hour24 := p.tok.value == 24
		tm.add(p.take().value)
		if !p.skipSymbol(':') {
			return invalid
		}
		if !p.tok.isFixedNumber(2) || !isMinute(p.tok.value) || (hour24 && p.tok.value > 0) {
			return invalid
		}
		tm.add(p.take().value)
		if p.skipSymbol(':') {
			if !p.tok.isFixedNumber(2) || !isMinute(p.tok.value) || (hour24 && p.tok.value > 0) {
				return invalid
			}
			tm.add(p.take().value)
			if p.skipSymbol('.') {
				if !p.tok.isNumber() || (hour24 && p.tok.value > 0) {
					return invalid
				}
				tm.add(readMilliseconds(p.take())) // more or fewer than three digits
			}
		}
		switch {
		case p.tok.isKeywordZ():
			p.take()
			tz.set(0)
		case p.tok.isSign():
			tz.setSign(p.take().sign())
			if p.tok.isFixedNumber(4) { // the hhmm extension
				hm := p.take().value
				if !isHour(hm/100) || !isMinute(hm%100) {
					return invalid
				}
				tz.hour, tz.minute = hm/100, hm%100
			} else {
				if !p.tok.isFixedNumber(2) || !isHour(p.tok.value) {
					return invalid
				}
				tz.hour = p.take().value
				if !p.skipSymbol(':') {
					return invalid
				}
				if !p.tok.isFixedNumber(2) || !isMinute(p.tok.value) {
					return invalid
				}
				tz.minute = p.take().value
			}
		}
		if p.tok.tag != tagEnd {
			return invalid
		}
	}
	// A date alone is UTC; a date-time without a zone is local time.
	if tz.hour == dpNone && tm.empty() {
		tz.set(0)
	}
	day.iso = true
	return dateToken{tagEnd, 0, -1}
}
