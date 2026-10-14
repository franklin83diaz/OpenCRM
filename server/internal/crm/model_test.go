package crm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"opencrm/internal/clawnify"
)

type call struct {
	auth, path string
	body       map[string]any
}

func fakeAI(t *testing.T, replies ...func(w http.ResponseWriter)) (*httptest.Server, *[]call) {
	var calls []call
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, &body)
		calls = append(calls, call{r.Header.Get("Authorization"), r.URL.Path, body})
		i := len(calls) - 1
		if i >= len(replies) {
			i = len(replies) - 1
		}
		replies[i](w)
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func ok(content string) func(http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": content}}}})
	}
}

func failing(status int, msg string) func(http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"message": msg}})
	}
}

func TestCompleteJSONMode(t *testing.T) {
	srv, calls := fakeAI(t, ok(`{"a":1}`))
	got, err := Complete(context.Background(), clawnify.Env{Token: "t", APIURL: srv.URL + "/"}, "sys", "usr", CompleteOptions{Timeout: time.Second})
	if err != nil || got != `{"a":1}` {
		t.Fatalf("got=%q err=%v", got, err)
	}
	c := (*calls)[0]
	if c.auth != "Bearer t" || c.path != "/v1/chat/completions" {
		t.Fatalf("%+v", c)
	}
	want := `map[max_tokens:600 messages:[map[content:sys role:system] map[content:usr role:user]] model:google/gemini-3.1-flash-lite response_format:map[type:json_object] temperature:0.2]`
	if s := sprint(c.body); s != want {
		t.Fatalf("body\n%s\nwant\n%s", s, want)
	}
}

func TestCompleteResearchRetriesInRegion(t *testing.T) {
	srv, calls := fakeAI(t, failing(400, "External search is not allowed in this Data Region"), ok("answer"))
	got, err := Complete(context.Background(), clawnify.Env{Token: "t", APIURL: srv.URL}, "s", "u", CompleteOptions{Research: true, MaxTokens: 1500, Timeout: time.Second})
	if err != nil || got != "answer" {
		t.Fatalf("got=%q err=%v", got, err)
	}
	if len(*calls) != 2 {
		t.Fatalf("%d calls", len(*calls))
	}
	if s := sprint((*calls)[0].body["plugins"]); s != "[map[id:web max_results:5]]" || (*calls)[0].body["response_format"] != nil || (*calls)[0].body["max_tokens"] != float64(1500) {
		t.Fatalf("first %v", (*calls)[0].body)
	}
	second := (*calls)[1].body
	if second["model"] != "google/gemini-3.5-flash-lite" || sprint(second["plugins"]) != "[map[engine:native id:web]]" {
		t.Fatalf("second %v", second)
	}
}

func TestCompleteErrors(t *testing.T) {
	cases := []struct {
		name     string
		reply    func(http.ResponseWriter)
		research bool
		want     string
		credits  bool
	}{
		{"credits", failing(402, "x"), false, "Out of Clawnify credits", true},
		{"message", failing(500, "model down"), false, "model down", false},
		{"no region retry without research", failing(400, "data region"), false, "data region", false},
		{"bare status", func(w http.ResponseWriter) { w.WriteHeader(503); io.WriteString(w, "<html>") }, false, "The AI service answered 503", false},
	}
	for _, c := range cases {
		srv, _ := fakeAI(t, c.reply)
		_, err := Complete(context.Background(), clawnify.Env{Token: "t", APIURL: srv.URL}, "s", "u", CompleteOptions{Research: c.research, Timeout: time.Second})
		var me *ModelError
		if !errors.As(err, &me) || me.Error() != c.want || me.OutOfCredits != c.credits {
			t.Errorf("%s: err=%v", c.name, err)
		}
	}
}

func TestCompleteNeedsToken(t *testing.T) {
	_, err := Complete(context.Background(), clawnify.Env{}, "s", "u", CompleteOptions{})
	var me *ModelError
	if !errors.As(err, &me) || me.Error() != "AI isn't available here: the app has no Clawnify token" || me.OutOfCredits {
		t.Fatalf("err=%v", err)
	}
}

func TestCompleteTimeout(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-release }))
	defer srv.Close()
	defer close(release)
	_, err := Complete(context.Background(), clawnify.Env{Token: "t", APIURL: srv.URL}, "s", "u", CompleteOptions{Timeout: 20 * time.Millisecond})
	if err == nil {
		t.Fatal("no timeout")
	}
}

func TestModelConst(t *testing.T) {
	if Model != "google/gemini-3.1-flash-lite" {
		t.Fatal(Model)
	}
}

func sprint(v any) string { return fmt.Sprint(v) }
