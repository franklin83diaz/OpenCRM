package clawnify

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// ServicesError is ClawnifyServicesError.
type ServicesError struct {
	Message string
	Status  int
}

func (e *ServicesError) Error() string { return e.Message }

const defaultServicesURL = "https://services.clawnify.com"

// servicesClient is a variable so tests can swap it.
var servicesClient = http.DefaultClient

// MarkdownResult is a live page's content as markdown; the markdown is all
// the CRM reads of the service's answer.
type MarkdownResult struct {
	Markdown string `json:"markdown"`
}

// RenderMarkdown fetches url through the platform's browser and returns it as
// markdown, cut to maxChars (0 leaves the cut to the service). The context
// carries the caller's time budget, where TS passed an AbortSignal.
func RenderMarkdown(ctx context.Context, env Env, url string, maxChars int) (MarkdownResult, error) {
	var zero MarkdownResult
	if env.Token == "" {
		return zero, &ServicesError{Message: "CLAWNIFY_TOKEN is not set, so Clawnify services are unavailable."}
	}
	base := env.ServicesURL
	if base == "" {
		base = defaultServicesURL
	}
	const path = "/markdown/render"
	body := map[string]any{"url": url}
	if maxChars != 0 {
		body["max_chars"] = maxChars
	}
	status, raw, err := doJSON(ctx, servicesClient, "POST", trimSlashes(base)+path, map[string]string{
		"Authorization": "Bearer " + env.Token,
		"Content-Type":  "application/json",
	}, body)
	if err != nil {
		return zero, err
	}
	if status < 200 || status > 299 {
		var answer map[string]any
		_ = json.Unmarshal(raw, &answer)
		detail, _ := answer["detail"].(string)
		code, _ := answer["error"].(string)
		msg := detail
		if msg == "" {
			msg = code
		}
		if msg == "" {
			msg = fmt.Sprintf("%s answered %d", path, status)
		}
		return zero, &ServicesError{Message: msg, Status: status}
	}
	var out MarkdownResult
	if err := json.Unmarshal(raw, &out); err != nil {
		return zero, err
	}
	return out, nil
}
