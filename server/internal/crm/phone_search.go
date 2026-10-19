package crm

import (
	"fmt"
	"regexp"
	"strings"
)

// Searching by phone number: a number typed any way ("5551234567", "555-123",
// "(555) 123") finds one stored any way ("+1 (555) 123-4567"), both sides
// compared as bare digits.

// phoneShape is a search that is a phone number, typed any way: digits and
// the marks people write them with.
var phoneShape = regexp.MustCompile(`^[0-9\s()+.\-]+$`)

// phoneDigits is a search's digits when it is a phone number of 3 digits or
// more, else "".
func phoneDigits(search string) string {
	if !phoneShape.MatchString(search) {
		return ""
	}
	digits := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, search)
	if len(digits) < 3 {
		return ""
	}
	return digits
}

// bareDigitsSQL is column without the marks phone numbers are written with,
// so "(555) 123-4567" and "+1 555.123.4567" compare as digits.
func bareDigitsSQL(column string) string {
	out := column
	for _, mark := range []string{" ", "-", "(", ")", "+", "."} {
		out = fmt.Sprintf("REPLACE(%s, '%s', '')", out, mark)
	}
	return out
}
