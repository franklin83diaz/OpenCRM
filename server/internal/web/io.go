package web

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
)

// JSON writes v as c.json(v, status) did: no HTML escaping ("Cedar & Co" stays
// as is), NaN and ±Infinity as null, and no trailing newline. Map keys come
// out sorted, where JS kept insertion order; use a struct when the order of a
// body's fields matters.
func JSON(w http.ResponseWriter, status int, v any) error {
	b, err := marshal(v)
	if isNonFinite(err) {
		b, err = marshalNonFinite(v)
	}
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write(b)
	return nil // a failed write means the client left; nothing to report to it
}

// marshal is json.Marshal without HTML escaping.
func marshal(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

// Text writes body with the given content type.
func Text(w http.ResponseWriter, status int, contentType, body string) error {
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(status)
	io.WriteString(w, body)
	return nil
}

// cachedBody replaces r.Body once it has been read, so Body and RawBody can
// both be called on one request, as c.req.json() and c.req.text() could.
type cachedBody struct {
	*bytes.Reader
	raw []byte
}

func (cachedBody) Close() error { return nil }

func readBody(r *http.Request) []byte {
	if c, ok := r.Body.(cachedBody); ok {
		return c.raw
	}
	var raw []byte
	if r.Body != nil {
		raw, _ = io.ReadAll(r.Body) // a broken upload reads as what arrived, as in TS
		r.Body.Close()
	}
	r.Body = cachedBody{bytes.NewReader(raw), raw}
	return raw
}

// RawBody is c.req.text().
func RawBody(r *http.Request) string {
	return string(readBody(r))
}

// Body is c.req.json() with the usual .catch(): the parsed JSON (maps,
// []any, float64, string, bool), or nil when the body is empty or not JSON.
func Body(r *http.Request) any {
	var v any
	if err := json.Unmarshal(readBody(r), &v); err != nil {
		return nil
	}
	return v
}

// Object is the body as an object: `await c.req.json().catch(() => ({}))`
// followed by property reads. Anything that isn't a JSON object reads as {}.
func Object(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return map[string]any{}
}

// Issue is one zod 3 issue, with zod's field names. Fields are in zod's order
// for invalid_type, the most common one; the rest are only sent when set.
type Issue struct {
	Code       string   `json:"code"`
	Expected   any      `json:"expected,omitempty"`
	Received   any      `json:"received,omitempty"`
	Path       []any    `json:"path"`
	Message    string   `json:"message"`
	Minimum    any      `json:"minimum,omitempty"`
	Maximum    any      `json:"maximum,omitempty"`
	Type       string   `json:"type,omitempty"`
	Inclusive  *bool    `json:"inclusive,omitempty"`
	Exact      *bool    `json:"exact,omitempty"`
	Options    []string `json:"options,omitempty"`
	Validation any      `json:"validation,omitempty"` // invalid_string: "email", "url", "regex", …
}

type zodBody struct {
	Success bool     `json:"success"`
	Error   zodError `json:"error"`
}

type zodError struct {
	Issues []Issue `json:"issues"`
	Name   string  `json:"name"`
}

// ZodError answers a failed request validation the way @hono/zod-openapi's
// default hook did: 400 {"success":false,"error":{"issues":[…],"name":"ZodError"}}.
func ZodError(w http.ResponseWriter, issues []Issue) error {
	out := make([]Issue, len(issues))
	for i, is := range issues {
		if is.Path == nil {
			is.Path = []any{} // zod's root path is [], never null
		}
		out[i] = is
	}
	return JSON(w, http.StatusBadRequest, zodBody{Error: zodError{Issues: out, Name: "ZodError"}})
}
