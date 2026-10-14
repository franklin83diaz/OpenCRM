package web_test

import (
	"encoding/json"
	"errors"
	"math"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"opencrm/internal/web"
)

// testdata/v8_json_errors.json is JSON.parse run by node 22 on each input:
// {input, error} with error null when the text parses. The Workers runtime
// words these errors the same way (the parity run shows its 500 bodies).
func TestParseJSONWordsErrorsAsV8(t *testing.T) {
	raw, err := os.ReadFile("testdata/v8_json_errors.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Input string  `json:"input"`
		Error *string `json:"error"`
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		_, err := web.ParseJSON(c.Input)
		var got *string
		var se *web.JSONSyntaxError
		if errors.As(err, &se) {
			got = &se.Message
		} else if err != nil {
			t.Errorf("%q: unexpected error type %T: %v", c.Input, err, err)
			continue
		}
		if c.Error != nil && strings.ContainsRune(*c.Error, utf8.RuneError) {
			// V8 named a lone surrogate (half an emoji); Go strings cannot hold one.
			continue
		}
		if !reflect.DeepEqual(got, c.Error) {
			t.Errorf("%q:\n got  %v\n want %v", c.Input, deref(got), deref(c.Error))
		}
	}
}

func deref(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}

func TestParseJSONValues(t *testing.T) {
	v, err := web.ParseJSON(`{"a":[1e400,-0,"x"],"b":null}`)
	if err != nil {
		t.Fatal(err)
	}
	a := v.(map[string]any)["a"].([]any)
	if !math.IsInf(a[0].(float64), 1) || a[2] != "x" {
		t.Errorf("got %#v", a)
	}
	if _, ok := v.(map[string]any)["b"]; !ok {
		t.Error("null member dropped")
	}
}

func TestJSONBody(t *testing.T) {
	r := httptest.NewRequest("POST", "/", strings.NewReader("\xef\xbb\xbf{\"a\":1}"))
	v, err := web.JSONBody(r)
	if err != nil || v.(map[string]any)["a"] != 1.0 {
		t.Errorf("BOM body: %v, %v", v, err)
	}
	r = httptest.NewRequest("POST", "/", strings.NewReader(""))
	if _, err := web.JSONBody(r); err == nil || err.Error() != "Unexpected end of JSON input" {
		t.Errorf("empty body: %v", err)
	}
	r = httptest.NewRequest("POST", "/", strings.NewReader("null"))
	if v, err := web.JSONBody(r); v != nil || err != nil {
		t.Errorf("null body: %v, %v", v, err)
	}
}
