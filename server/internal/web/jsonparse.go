package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf16"
)

// JSONSyntaxError is the SyntaxError JSON.parse threw, worded as V8 words it
// ("Unexpected end of JSON input", "Expected property name or '}' in JSON at
// position 1 (line 1 column 2)"). Routes that answered {error: err.message}
// put this text in the body.
type JSONSyntaxError struct{ Message string }

func (e *JSONSyntaxError) Error() string { return e.Message }

// JSONBody is c.req.json() without a .catch: the parsed body, or a
// *JSONSyntaxError when it is not JSON (an empty body included). JSON null is
// nil, which a route reading a property of it must treat as TS did (a TypeError).
func JSONBody(r *http.Request) (any, error) {
	// The fetch body's text() is a UTF-8 decode, which drops a leading BOM.
	return ParseJSON(string(bytes.TrimPrefix(readBody(r), []byte("\xef\xbb\xbf"))))
}

// ParseJSON is JSON.parse: maps, []any, float64, string, bool or nil. Numbers
// past float64's range are ±Infinity, as in JS, where encoding/json would fail.
func ParseJSON(text string) (any, error) {
	c := &jsonCheck{u: utf16.Encode([]rune(text))}
	if msg := c.check(); msg != "" {
		return nil, &JSONSyntaxError{Message: msg}
	}
	dec := json.NewDecoder(strings.NewReader(text))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err // the check above accepts only what decodes
	}
	return jsNumbers(v), nil
}

// jsNumbers turns json.Number into float64 the way JS reads a number literal.
func jsNumbers(v any) any {
	switch v := v.(type) {
	case json.Number:
		f, _ := strconv.ParseFloat(string(v), 64) // out of range: ±Inf, as JS
		return f
	case []any:
		for i := range v {
			v[i] = jsNumbers(v[i])
		}
	case map[string]any:
		for k := range v {
			v[k] = jsNumbers(v[k])
		}
	}
	return v
}

// jsonCheck walks a JSON text as V8's JsonParser does, only to find the first
// error and word it the same way. Positions count UTF-16 code units, as JS
// string indexes do.
type jsonCheck struct {
	u   []uint16
	pos int
}

const v8EndOfInput = "Unexpected end of JSON input"

func (c *jsonCheck) check() string {
	c.skipSpace()
	if msg := c.value(); msg != "" {
		return msg
	}
	c.skipSpace()
	if !c.atEnd() {
		line, col := c.location()
		return fmt.Sprintf("Unexpected non-whitespace character after JSON at position %d (line %d column %d)", c.pos, line, col)
	}
	return ""
}

func (c *jsonCheck) atEnd() bool { return c.pos >= len(c.u) }

func (c *jsonCheck) peek() uint16 {
	if c.atEnd() {
		return 0
	}
	return c.u[c.pos]
}

// skipSpace skips JSON's four whitespace characters (not JS's \s).
func (c *jsonCheck) skipSpace() {
	for !c.atEnd() {
		switch c.u[c.pos] {
		case ' ', '\t', '\n', '\r':
			c.pos++
		default:
			return
		}
	}
}

func isDigit(ch uint16) bool { return ch >= '0' && ch <= '9' }

func (c *jsonCheck) value() string {
	if c.atEnd() {
		return v8EndOfInput
	}
	switch ch := c.u[c.pos]; {
	case ch == '{':
		return c.object()
	case ch == '[':
		return c.array()
	case ch == '"':
		return c.str()
	case ch == '-' || isDigit(ch):
		return c.number()
	case ch == 't':
		return c.literal("true")
	case ch == 'f':
		return c.literal("false")
	case ch == 'n':
		return c.literal("null")
	}
	return c.unexpected()
}

func (c *jsonCheck) object() string {
	c.pos++ // {
	c.skipSpace()
	if c.peek() == '}' && !c.atEnd() {
		c.pos++
		return ""
	}
	if c.atEnd() || c.peek() != '"' {
		return c.at("Expected property name or '}'")
	}
	for {
		if msg := c.str(); msg != "" {
			return msg
		}
		c.skipSpace()
		if c.atEnd() || c.peek() != ':' {
			return c.at("Expected ':' after property name")
		}
		c.pos++
		c.skipSpace()
		if msg := c.value(); msg != "" {
			return msg
		}
		c.skipSpace()
		if !c.atEnd() && c.peek() == '}' {
			c.pos++
			return ""
		}
		if c.atEnd() || c.peek() != ',' {
			return c.at("Expected ',' or '}' after property value")
		}
		c.pos++
		c.skipSpace()
		if c.atEnd() || c.peek() != '"' {
			return c.at("Expected double-quoted property name")
		}
	}
}

func (c *jsonCheck) array() string {
	c.pos++ // [
	c.skipSpace()
	if !c.atEnd() && c.peek() == ']' {
		c.pos++
		return ""
	}
	for {
		if msg := c.value(); msg != "" {
			return msg
		}
		c.skipSpace()
		if !c.atEnd() && c.peek() == ']' {
			c.pos++
			return ""
		}
		if c.atEnd() || c.peek() != ',' {
			return c.at("Expected ',' or ']' after array element")
		}
		c.pos++
		c.skipSpace()
	}
}

func (c *jsonCheck) str() string {
	c.pos++ // opening quote
	for {
		if c.atEnd() {
			return c.at("Unterminated string")
		}
		ch := c.u[c.pos]
		switch {
		case ch == '"':
			c.pos++
			return ""
		case ch < 0x20:
			return c.at("Bad control character in string literal")
		case ch == '\\':
			c.pos++
			if c.atEnd() {
				return v8EndOfInput
			}
			switch e := c.u[c.pos]; {
			case e == '"' || e == '\\' || e == '/' || e == 'b' || e == 'f' || e == 'n' || e == 'r' || e == 't':
			case e == 'u':
				for i := 0; i < 4; i++ {
					c.pos++
					if c.atEnd() || !isHex(c.u[c.pos]) {
						return c.at("Bad Unicode escape")
					}
				}
			case e > 0xFF:
				return c.unexpected() // beyond Latin-1 V8 reports the character itself
			default:
				return c.at("Bad escaped character")
			}
		}
		c.pos++
	}
}

func isHex(ch uint16) bool {
	return isDigit(ch) || (ch >= 'a' && ch <= 'f') || (ch >= 'A' && ch <= 'F')
}

func (c *jsonCheck) number() string {
	if c.peek() == '-' {
		c.pos++
	}
	if !c.atEnd() && c.peek() == '0' {
		c.pos++
		if !c.atEnd() && isDigit(c.peek()) {
			return c.at("Unexpected number") // a leading zero must stand alone
		}
	} else {
		if c.atEnd() || !isDigit(c.peek()) {
			return c.at("No number after minus sign")
		}
		c.digits()
	}
	if !c.atEnd() && c.peek() == '.' {
		c.pos++
		if c.atEnd() || !isDigit(c.peek()) {
			return c.at("Unterminated fractional number")
		}
		c.digits()
	}
	if !c.atEnd() && (c.peek() == 'e' || c.peek() == 'E') {
		c.pos++
		if !c.atEnd() && (c.peek() == '+' || c.peek() == '-') {
			c.pos++
		}
		if c.atEnd() || !isDigit(c.peek()) {
			return c.at("Exponent part is missing a number")
		}
		c.digits()
	}
	return ""
}

func (c *jsonCheck) digits() {
	for !c.atEnd() && isDigit(c.peek()) {
		c.pos++
	}
}

// literal checks the rest of true/false/null; the first letter already matched.
func (c *jsonCheck) literal(word string) string {
	for i := 0; i < len(word); i++ {
		if c.atEnd() {
			return v8EndOfInput
		}
		if c.u[c.pos] != uint16(word[i]) {
			return c.unexpected()
		}
		c.pos++
	}
	return ""
}

// at words a message that carries where it happened.
func (c *jsonCheck) at(msg string) string {
	line, col := c.location()
	return fmt.Sprintf("%s in JSON at position %d (line %d column %d)", msg, c.pos, line, col)
}

// location is V8's CalculateFileLocation: \r\n, \r and \n each end a line.
func (c *jsonCheck) location() (line, col int) {
	line = 1
	lastBreak := 0
	i := 0
	for ; i < c.pos; i++ {
		if c.u[i] == '\r' && i < c.pos-1 && c.u[i+1] == '\n' {
			i++
		}
		if c.u[i] == '\r' || c.u[i] == '\n' {
			line++
			lastBreak = i + 1
		}
	}
	return line, 1 + i - lastBreak
}

// unexpected is LookUpErrorMessageForJsonToken: the character at pos read as a
// token nothing expected there.
func (c *jsonCheck) unexpected() string {
	if c.atEnd() {
		return v8EndOfInput
	}
	ch := c.u[c.pos]
	if ch == '"' {
		return c.at("Unexpected string")
	}
	if ch == '-' || isDigit(ch) {
		return c.at("Unexpected number")
	}
	src := string(utf16.Decode(c.u))
	switch src {
	case "[object Object]", "undefined", "Infinity", "NaN":
		return `"` + src + `" is not valid JSON`
	}
	token := string(utf16.Decode([]uint16{ch}))
	// V8 quotes the whole text when it is short, else ten characters either side.
	const context = 10
	n := len(c.u)
	sub := func(from, to int) string { return string(utf16.Decode(c.u[from:to])) }
	switch {
	case n < 2*context+1:
		return fmt.Sprintf(`Unexpected token '%s', "%s" is not valid JSON`, token, src)
	case c.pos < context:
		return fmt.Sprintf(`Unexpected token '%s', "%s"... is not valid JSON`, token, sub(0, c.pos+context))
	case c.pos < n-context:
		return fmt.Sprintf(`Unexpected token '%s', ..."%s"... is not valid JSON`, token, sub(c.pos-context, c.pos+context))
	default:
		return fmt.Sprintf(`Unexpected token '%s', ..."%s" is not valid JSON`, token, sub(c.pos-context, n))
	}
}
