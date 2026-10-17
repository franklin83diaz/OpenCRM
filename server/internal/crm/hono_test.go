package crm

import (
	"net/http/httptest"
	"testing"
)

func TestHonoQueries(t *testing.T) {
	// Expectations from hono/utils/url getQueryParams on the same URL.
	r := httptest.NewRequest("GET", "/x?link=un%6Datched&a=1+2&a=%E0%A4%A&=skip&flag&b=x=y&c=%41%E0%A4&d=%41-%E0%A4", nil)
	got := honoQueries(r)
	want := map[string][]string{"link": {"unmatched"}, "a": {"1 2", "%E0%A4%A"}, "flag": {""}, "b": {"x=y"}, "c": {"%41%E0%A4"}, "d": {"A-%E0%A4"}}
	if canonical(got) != canonical(want) {
		t.Errorf("honoQueries %s, want %s", canonical(got), canonical(want))
	}
	if q := queryInput(r); canonical(q["a"]) != `["1 2","%E0%A4%A"]` || q["link"] != "unmatched" {
		t.Errorf("queryInput %v", q)
	}
	for ct, isJSON := range map[string]bool{
		"application/json": true, "application/json; charset=utf-8": true, "application/vnd.api+json": true,
		"application/json;charset=UTF-8": true, "Application/JSON": false, "application/json;": false, "text/plain": false, "": false,
	} {
		if got := jsonContentType.MatchString(ct); got != isJSON {
			t.Errorf("JSON content type %q: %v", ct, got)
		}
	}
}
