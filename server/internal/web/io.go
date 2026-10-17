package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

// JSON writes v as c.json(v, status) did: no HTML escaping ("Cedar & Co" stays
// as is), NaN and ±Infinity as null, and no trailing newline. Map keys come
// out sorted, where JS kept insertion order; use a struct when the order of a
// body's fields matters. Like Text, it tells browsers not to sniff the
// content type (X-Content-Type-Options: nosniff).
func JSON(w http.ResponseWriter, status int, v any) error {
	b, err := marshal(v)
	if isNonFinite(err) {
		b, err = marshalNonFinite(v)
	}
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
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

// Text writes body with the given content type, not to be sniffed.
func Text(w http.ResponseWriter, status int, contentType, body string) error {
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	io.WriteString(w, body)
	return nil
}

// MaxBody is the most of a request body the API reads: 25 MiB. The Router
// caps every request at it (the login, at less). A body past it is not read
// on, and reads as one that isn't JSON: Body is nil, JSONBody is
// ErrBodyTooLarge and RawBody is "", so each route answers it as it answers
// an unparseable body.
const MaxBody = 25 << 20

// ErrBodyTooLarge is JSONBody's error for a body past its cap; a route that
// answered {error: err.message} for a body that isn't JSON says this instead.
var ErrBodyTooLarge = errors.New("Request body too large: the limit is 25 MiB")

// cachedBody replaces r.Body once it has been read, so Body and RawBody can
// both be called on one request, as c.req.json() and c.req.text() could.
type cachedBody struct {
	*bytes.Reader
	raw      []byte
	tooLarge bool
}

func (cachedBody) Close() error { return nil }

// readBody is the request body, read once; tooLarge when it went past its
// cap (http.MaxBytesReader: MaxBody, or the route's own), and then no body.
func readBody(r *http.Request) (raw []byte, tooLarge bool) {
	if c, ok := r.Body.(cachedBody); ok {
		return c.raw, c.tooLarge
	}
	if r.Body != nil {
		var err error
		raw, err = io.ReadAll(r.Body) // a broken upload reads as what arrived, as in TS
		var tooBig *http.MaxBytesError
		if tooLarge = errors.As(err, &tooBig); tooLarge {
			raw = nil
		}
		r.Body.Close()
	}
	r.Body = cachedBody{bytes.NewReader(raw), raw, tooLarge}
	return raw, tooLarge
}

// RawBody is c.req.text(); "" for a body past its cap.
func RawBody(r *http.Request) string {
	raw, _ := readBody(r)
	return string(raw)
}

// Body is c.req.json() with the usual .catch(): the parsed JSON (maps,
// []any, float64, string, bool), or nil when the body is empty, not JSON or
// past its cap.
func Body(r *http.Request) any {
	raw, _ := readBody(r)
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
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
// Keys, when set, are the fields written and their order, for an issue zod
// built in an order of its own.
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
	// UnionErrors: invalid_union's ZodErrors, one per option of the union.
	UnionErrors []UnionError `json:"unionErrors,omitempty"`
	// Keys: e.g. ZodEnum's invalid_enum_value is {received, code, options,
	// path, message}, and JSON.stringify kept that order.
	Keys []string `json:"-"`
}

// MarshalJSON writes the issue's fields, in Keys' order when it has Keys.
func (is Issue) MarshalJSON() ([]byte, error) {
	type plain Issue // the same fields, without this method
	b, err := marshal(plain(is))
	if isNonFinite(err) {
		b, err = marshalNonFinite(plain(is))
	}
	if err != nil || is.Keys == nil {
		return b, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(b, &fields); err != nil {
		return nil, err
	}
	var out bytes.Buffer
	out.WriteByte('{')
	for _, k := range is.Keys {
		raw, ok := fields[k]
		if !ok {
			continue
		}
		if out.Len() > 1 {
			out.WriteByte(',')
		}
		name, _ := marshal(k)
		out.Write(name)
		out.WriteByte(':')
		out.Write(raw)
	}
	out.WriteByte('}')
	return out.Bytes(), nil
}

// UnionError is one option's ZodError inside an invalid_union issue, as
// JSON.stringify wrote it: its issues, then its name.
type UnionError struct {
	Issues []Issue `json:"issues"`
	Name   string  `json:"name"`
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
