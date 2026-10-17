package crm

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

// jsonOf is JSON.stringify: no HTML escaping, no trailing newline.
func jsonOf(t *testing.T, v any) string {
	t.Helper()
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		t.Fatal(err)
	}
	return string(bytes.TrimSuffix(b.Bytes(), []byte("\n")))
}

func TestRulesBlocklist(t *testing.T) {
	got := NormaliseBlocklist([]any{" Ada@X.com", "@Y.com", "z.com", "", 3.0, "@@w.com", "a@b.com", "@y.com"})
	want := []string{"ada@x.com", "@y.com", "@z.com", "@@w.com", "a@b.com"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("NormaliseBlocklist = %v, want %v", got, want)
	}
	if got := NormaliseBlocklist("nope"); got == nil || len(got) != 0 {
		t.Errorf("non-array = %#v", got)
	}
	if !IsBlocked("ada@x.com", got) || !IsBlocked("who@y.com", got) || IsBlocked("who@q.com", got) {
		t.Error("IsBlocked")
	}
}

func jsISO(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000Z") }
