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

func TestRulesParseAddresses(t *testing.T) {
	cases := []struct{ header, want string }{
		{`"Ann Lee" <ann@x.com>, bob@y.org`, `[{"email":"ann@x.com","name":"Ann Lee"},{"email":"bob@y.org","name":null}]`},
		{`"Lee, Ann" <ANN@x.com>, mailto:Bob@y.org, <c@d.com>, Dee <dee@d.com>, junk, ann@x.com, "x@y.com" <x@y.com>`,
			`[{"email":"ann@x.com","name":"Lee, Ann"},{"email":"bob@y.org","name":null},{"email":"c@d.com","name":null},{"email":"dee@d.com","name":"Dee"},{"email":"x@y.com","name":null}]`},
		{``, `[]`},
		{`no address here`, `[]`},
	}
	for _, c := range cases {
		if got := jsonOf(t, ParseAddresses(c.header)); got != c.want {
			t.Errorf("ParseAddresses(%q)\n got %s\nwant %s", c.header, got, c.want)
		}
	}
}

func TestRulesGroupAddress(t *testing.T) {
	emails := []string{"noreply@x.com", "no-reply@x.com", "Team@x.com", "bounces+abc@x.com", "notifications-x@y.com", "ann@x.com",
		"donotreply@x.com", "do-not-reply@x.com", "bounce@x.com", "notification@y.com", "xnotifications+@y.com", "plain"}
	want := []bool{true, true, true, true, true, false, true, true, true, true, false, false}
	for i, e := range emails {
		if got := IsGroupAddress(e); got != want[i] {
			t.Errorf("IsGroupAddress(%q) = %v, want %v", e, got, want[i])
		}
	}
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

func TestRulesMayCreate(t *testing.T) {
	personal := func(d string) bool { return d == "gmail.com" }
	rules := CreateRules{Mailbox: "me@mine.com", Policy: "sent", ExcludeGroup: true, ExcludePersonal: true,
		Blocklist: []string{"@spam.com", "bad@ok.com"}, IsPersonalDomain: personal}
	cases := []struct {
		email string
		want  bool
	}{
		{"ann@x.com", true},
		{"nodomain", false},
		{"me@mine.com", false},
		{"colleague@mine.com", false},
		{"a@spam.com", false},
		{"bad@ok.com", false},
		{"support@x.com", false},
		{"ann@gmail.com", false},
	}
	for _, c := range cases {
		if got := MayCreate(Address{Email: c.email}, rules); got != c.want {
			t.Errorf("MayCreate(%q) = %v, want %v", c.email, got, c.want)
		}
	}
	loose := CreateRules{Mailbox: "me@gmail.com", Policy: "sent", IsPersonalDomain: personal}
	if !MayCreate(Address{Email: "friend@gmail.com"}, loose) {
		t.Error("personal mailbox has no team domain to skip")
	}
	if !MayCreate(Address{Email: "support@x.com"}, loose) {
		t.Error("group mail allowed when not excluded")
	}
}

func TestRulesCreationCandidates(t *testing.T) {
	personal := func(d string) bool { return d == "gmail.com" }
	ann, bob := Address{Email: "ann@x.com"}, Address{Email: "bob@y.com"}
	sent := CandidateMessage{Direction: "sent", From: &Address{Email: "me@mine.com"}, To: []Address{ann, {Email: "colleague@mine.com"}, bob}}
	recv := CandidateMessage{Direction: "received", From: &ann, To: []Address{{Email: "me@mine.com"}}, LabelIDs: []string{"INBOX"}}
	bulk := CandidateMessage{Direction: "received", From: &ann, LabelIDs: []string{"INBOX", "CATEGORY_PROMOTIONS"}}
	rules := func(p AutoCreate) CreateRules {
		return CreateRules{Mailbox: "me@mine.com", Policy: p, IsPersonalDomain: personal}
	}
	cases := []struct {
		name   string
		msg    CandidateMessage
		policy AutoCreate
		want   string
	}{
		{"none", sent, "none", `[]`},
		{"sent recipients", sent, "sent", `[{"email":"ann@x.com","name":null},{"email":"bob@y.com","name":null}]`},
		{"received ignored on sent", recv, "sent", `[]`},
		{"received sender", recv, "sent_and_received", `[{"email":"ann@x.com","name":null}]`},
		{"bulk tab", bulk, "sent_and_received", `[]`},
		{"received without sender", CandidateMessage{Direction: "received"}, "sent_and_received", `[]`},
	}
	for _, c := range cases {
		if got := jsonOf(t, CreationCandidates(c.msg, rules(c.policy))); got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}
}

func TestRulesSplitName(t *testing.T) {
	names := []string{"Ada Lovelace", "Lovelace, Ada", "Lovelace,", "a,b,c", "  ", "x@y.com", "Mary Jane Watson", "\u0085", "Ada\ufeffLovelace", "Ada\u0085Lovelace"}
	want := []string{
		`{"first":"Ada","last":"Lovelace"}`, `{"first":"Ada","last":"Lovelace"}`, `{"first":"Lovelace,","last":""}`,
		`{"first":"b","last":"a"}`, `{"first":"ada","last":""}`, `{"first":"ada","last":""}`, `{"first":"Mary","last":"Jane Watson"}`,
		"{\"first\":\"\u0085\",\"last\":\"\"}", `{"first":"Ada","last":"Lovelace"}`, "{\"first\":\"Ada\u0085Lovelace\",\"last\":\"\"}",
	}
	for i, n := range names {
		n := n
		if got := jsonOf(t, SplitName(Address{Email: "ada@x.com", Name: &n})); got != want[i] {
			t.Errorf("SplitName(%q) = %s, want %s", n, got, want[i])
		}
	}
	if got := SplitName(Address{Email: "ada@x.com"}); got != (PersonName{First: "ada"}) {
		t.Errorf("no name: %v", got)
	}
}

func TestRulesQueries(t *testing.T) {
	since := time.Date(2026, 1, 5, 10, 0, 0, 0, time.UTC)
	sc := Scope{Labels: []string{"Clients 2024", "A/B  c"}, Since: &since}
	check := func(name, got, want string) {
		t.Helper()
		if got != want {
			t.Errorf("%s\n got %s\nwant %s", name, got, want)
		}
	}
	check("sent", SentQuery(sc), "in:sent -in:chats {label:clients-2024 label:a-b-c} after:2026/01/05")
	check("received", ReceivedQuery(Scope{Labels: []string{}}), "-in:sent -category:promotions -category:social -category:updates -category:forums -in:chats")
	check("people", PeopleQuery([]string{"a@x.com", "b@y.com"}, Scope{Labels: []string{"One"}}), "{from:a@x.com to:a@x.com from:b@y.com to:b@y.com} -in:chats label:one")

	until := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	recent := time.Date(2026, 10, 14, 11, 30, 0, 0, time.UTC)
	old := time.Date(2026, 10, 14, 10, 30, 0, 0, time.UTC)
	check("since live", SinceQuery(until, Scope{}), "-in:chats after:1791975600")
	check("since labels", SinceQuery(until, Scope{Labels: []string{"x"}}), "-in:chats label:x after:1791892800")
	check("since floor wins", SinceQuery(until, Scope{Since: &recent}), "-in:chats after:1791977400")
	check("since overlap wins", SinceQuery(until, Scope{Since: &old}), "-in:chats after:1791975600")

	if got := HistoryStart("3m", until); got == nil || jsISO(*got) != "2026-07-16T12:00:00.000Z" {
		t.Errorf("HistoryStart 3m = %v", got)
	}
	if got := HistoryStart("12m", until); got == nil || !got.Equal(until.Add(-365*24*time.Hour)) {
		t.Errorf("HistoryStart 12m = %v", got)
	}
	if HistoryStart("all", until) != nil {
		t.Error("HistoryStart all must be nil")
	}
}

func jsISO(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000Z") }
