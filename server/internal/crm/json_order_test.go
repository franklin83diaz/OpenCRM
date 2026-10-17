package crm

import (
	"slices"
	"testing"

	"opencrm/internal/web"
)

// TestJSONOrderStringify: jsStringify of a parsed JSON text with the text's
// key order is JSON.stringify(JSON.parse(text)) — array-index keys first, a
// repeated key in its first place with its last value, numbers as JS writes
// them, strings escaped as V8 does. Expectations from node 22.
func TestJSONOrderStringify(t *testing.T) {
	for _, c := range []struct{ text, want string }{
		{"{\"b\":1,\"a\":2}", "{\"b\":1,\"a\":2}"},
		{"{\"z\":1,\"10\":2,\"2\":3,\"a\":{\"y\":1,\"b\":2},\"01\":4,\"4294967295\":5,\"4294967294\":6}", "{\"2\":3,\"10\":2,\"4294967294\":6,\"z\":1,\"a\":{\"y\":1,\"b\":2},\"01\":4,\"4294967295\":5}"},
		{"{\"a\":1,\"b\":2,\"a\":3}", "{\"a\":3,\"b\":2}"},
		{"{\"a\":{\"x\":1,\"y\":2},\"a\":{\"y\":3,\"x\":4}}", "{\"a\":{\"y\":3,\"x\":4}}"},
		{"[{\"b\":1,\"a\":2},[{\"d\":1,\"c\":2}],null,true,false,\"s\"]", "[{\"b\":1,\"a\":2},[{\"d\":1,\"c\":2}],null,true,false,\"s\"]"},
		{"[1e400,-1e400,-0,0.1,1e21,1e-7,123456789012345680000,5e-324]", "[null,null,0,0.1,1e+21,1e-7,123456789012345680000,5e-324]"},
		{"\"x\\u2028\\u2029\\u0007\\u001f\\b\\f\\n\\r\\t\\\"\\\\\\/<>&\\u00e9\\ud83c\\udf89\"", "\"x\u2028\u2029\\u0007\\u001f\\b\\f\\n\\r\\t\\\"\\\\/<>&é🎉\""},
		{"{\"__proto__\":{\"a\":1},\"constructor\":2}", "{\"__proto__\":{\"a\":1},\"constructor\":2}"},
		{" { \"spaced\" : [ 1 , 2 ] } ", "{\"spaced\":[1,2]}"},
		{"{\"\":1,\" \":2,\"-1\":3,\"1.5\":4}", "{\"\":1,\" \":2,\"-1\":3,\"1.5\":4}"},
	} {
		v, err := web.ParseJSON(c.text)
		if err != nil {
			t.Fatalf("%s: %v", c.text, err)
		}
		if got := jsStringify(v, readKeyOrder(c.text)); got != c.want {
			t.Errorf("%s\n got  %s\n want %s", c.text, got, c.want)
		}
	}
}

// TestJSONOrderUnknown: without an order (or for keys it doesn't know), keys
// come in JS order of the sorted keys.
func TestJSONOrderUnknown(t *testing.T) {
	v := map[string]any{"b": 1.0, "a": map[string]any{"d": 1.0, "c": 2.0}, "10": 1.0, "2": 1.0}
	if got, want := jsStringify(v, nil), `{"2":1,"10":1,"a":{"c":2,"d":1},"b":1}`; got != want {
		t.Errorf("no order: got %s, want %s", got, want)
	}
	o := readKeyOrder(`{"b":0}`)
	if got, want := o.orderOf(map[string]any{"b": 1.0, "a": 1.0, "3": 1.0}), []string{"3", "b", "a"}; !slices.Equal(got, want) {
		t.Errorf("partial order: got %v, want %v", got, want)
	}
	if readKeyOrder("not json") != nil || readKeyOrder("\xef\xbb\xbf{}") != nil {
		t.Error("an order from text that isn't JSON")
	}
}

// TestImportCompanyNameFromDomain: "acme.com" → "Acme", as
// label.charAt(0).toUpperCase() + label.slice(1) wrote it (node 22).
func TestImportCompanyNameFromDomain(t *testing.T) {
	for _, c := range []struct{ domain, want string }{
		{"acme.com", "Acme"},
		{"www.acme.com", "Acme"},
		{"www.", "Www."},
		{".lead.example", ".lead.example"},
		{"ßeta.example", "SSeta"},
		{"éclair.fr", "Éclair"},
		{"𐐨abc.example", "𐐨abc"},
		{"ǆx.example", "Ǆx"},
		{"1password.com", "1password"},
		{"WWW.x.com", "WWW"},
		{"a", "A"},
		{"", ""},
	} {
		if got := companyNameFromDomain(c.domain); got != c.want {
			t.Errorf("companyNameFromDomain(%q) = %q, want %q", c.domain, got, c.want)
		}
	}
}
