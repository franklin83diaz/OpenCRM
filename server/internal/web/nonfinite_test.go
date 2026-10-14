package web

import (
	"math"
	"reflect"
	"testing"
)

func TestSentinelsBecomeNullOnlyAsValues(t *testing.T) {
	s := newSentinels()
	tok, tok32 := string(s.tokens[0]), string(s.tokens[1])
	in := `{"a":` + tok + `,"b":"` + tok + `","c":[` + tok32 + `,1e-7,` + tok + `],"d":"\"` + tok + `\\"}`
	want := `{"a":null,"b":"` + tok + `","c":[null,1e-7,null],"d":"\"` + tok + `\\"}`
	if got := string(s.null([]byte(in))); got != want {
		t.Errorf("null(%s)\n got %s\nwant %s", in, got, want)
	}
	if got := string(s.null([]byte(tok))); got != "null" {
		t.Errorf("null(%s) = %s", tok, got)
	}

	// A real number written exactly like a sentinel makes it unusable.
	s.replace(reflect.ValueOf([]any{1.5, math.NaN()}), 0)
	if s.clash {
		t.Error("clash without a matching number")
	}
	s.replace(reflect.ValueOf([]any{s.f64, math.NaN()}), 0)
	if !s.clash {
		t.Error("a number equal to the sentinel was not flagged")
	}
}
