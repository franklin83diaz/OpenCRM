package crm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"opencrm/internal/clawnify"
)

// One completion from the Clawnify platform's model endpoint, with the org's
// token: each call is charged to the org's credits and follows its data
// region. Used by AI columns and by the meeting digest.

// Model is fast and inexpensive, and served in every data region the platform routes to.
const Model = "google/gemini-3.1-flash-lite"

// regionalSearchModel is the model research falls back to where only a
// model's own search is allowed (the platform's in-region search model).
const regionalSearchModel = "google/gemini-3.5-flash-lite"

// ModelError is a completion that could not be had; the message is shown to the user.
type ModelError struct {
	Message      string
	OutOfCredits bool
}

func (e *ModelError) Error() string { return e.Message }

// CompleteOptions are the settings of one completion.
type CompleteOptions struct {
	Research  bool
	MaxTokens int           // 0 is 600
	Timeout   time.Duration // for each request to the model; 0 is none
}

var dataRegion = regexp.MustCompile(`(?i)data region`)

type completion struct {
	ok      bool
	status  int
	err     string
	content string
}

// Complete asks the model system and user. With Research, the answer is
// grounded in a web search: OpenRouter's web plugin, billed with the call.
// Hosts pinned to a data region refuse external search engines and allow only
// a model's own search, so a refusal is retried once that way, on a model that
// has it in-region. Without it, the answer is asked for as JSON.
func Complete(ctx context.Context, env clawnify.Env, system, user string, opts CompleteOptions) (string, error) {
	if env.Token == "" {
		return "", &ModelError{Message: "AI isn't available here: the app has no Clawnify token"}
	}
	base := env.APIURL
	if base == "" {
		base = "https://api.clawnify.com"
	}
	base = strings.TrimRight(base, "/")
	maxTokens := opts.MaxTokens
	if maxTokens == 0 {
		maxTokens = 600
	}
	ask := func(extra map[string]any) (completion, error) {
		ctx := ctx // each request has its own time limit
		if opts.Timeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
			defer cancel()
		}
		payload := map[string]any{
			"model":       Model,
			"temperature": 0.2,
			"max_tokens":  maxTokens,
			"messages": []map[string]string{
				{"role": "system", "content": system},
				{"role": "user", "content": user},
			},
		}
		for k, v := range extra {
			payload[k] = v
		}
		body, err := json.Marshal(payload)
		if err != nil {
			return completion{}, err
		}
		req, err := http.NewRequestWithContext(ctx, "POST", base+"/v1/chat/completions", strings.NewReader(string(body)))
		if err != nil {
			return completion{}, err
		}
		req.Header.Set("Authorization", "Bearer "+env.Token)
		req.Header.Set("Content-Type", "application/json")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			return completion{}, err
		}
		defer res.Body.Close()
		if res.StatusCode == 402 {
			return completion{}, &ModelError{Message: "Out of Clawnify credits", OutOfCredits: true}
		}
		var parsed struct {
			Choices []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			} `json:"choices"`
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.NewDecoder(res.Body).Decode(&parsed) // an answer that is not JSON has no message either
		r := completion{ok: res.StatusCode >= 200 && res.StatusCode <= 299, status: res.StatusCode, err: parsed.Error.Message}
		if len(parsed.Choices) > 0 {
			r.content = parsed.Choices[0].Message.Content
		}
		return r, nil
	}
	// A web answer comes back as text around the JSON (the caller finds it), so no JSON mode then.
	first := map[string]any{"response_format": map[string]any{"type": "json_object"}}
	if opts.Research {
		first = map[string]any{"plugins": []any{map[string]any{"id": "web", "max_results": 5}}}
	}
	r, err := ask(first)
	if err != nil {
		return "", err
	}
	if !r.ok && opts.Research && dataRegion.MatchString(r.err) {
		r, err = ask(map[string]any{"model": regionalSearchModel, "plugins": []any{map[string]any{"id": "web", "engine": "native"}}})
		if err != nil {
			return "", err
		}
	}
	if !r.ok {
		msg := r.err
		if msg == "" {
			msg = fmt.Sprintf("The AI service answered %d", r.status)
		}
		return "", &ModelError{Message: msg}
	}
	return r.content, nil
}
