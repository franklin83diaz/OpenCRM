package crm

// What Hono and @hono/zod-openapi did to a request before an OpenAPI route's
// handler ran: the query string as c.req.queries() read it, the JSON body as
// the json validator read it, and zod 3's issues for what failed. Routes
// declared with app.openapi(createRoute(…)) validate with these; plain
// app.get/app.post routes never did.

import (
	"net/http"
	"regexp"
	"strings"

	"opencrm/internal/web"
)

// honoQueries is c.req.queries(): each parameter's values in the order sent,
// decoded as Hono did (a "+" is a space; a malformed escape is kept as sent,
// the rest of the value still decoded). A parameter with no name is dropped.
func honoQueries(r *http.Request) map[string][]string {
	out := map[string][]string{}
	for _, pair := range strings.Split(r.URL.RawQuery, "&") {
		name, value, hasValue := strings.Cut(pair, "=")
		name = honoDecode(name)
		if name == "" {
			continue
		}
		if hasValue {
			value = honoDecode(value)
		}
		out[name] = append(out[name], value)
	}
	return out
}

var escapeRun = regexp.MustCompile(`(?:%[0-9A-Fa-f]{2})+`)

// honoDecode is Hono's _decodeURI for query names and values.
func honoDecode(s string) string {
	if !strings.ContainsAny(s, "%+") {
		return s
	}
	s = strings.ReplaceAll(s, "+", " ")
	if !strings.Contains(s, "%") {
		return s
	}
	if d, err := decodeURIComponent(s); err == nil {
		return d
	}
	return escapeRun.ReplaceAllStringFunc(s, func(run string) string {
		if d, err := decodeURIComponent(run); err == nil {
			return d
		}
		return run
	})
}

// queryInput is what the query validator handed zod: a parameter sent once
// is its value, one sent more than once the list of them.
func queryInput(r *http.Request) map[string]any {
	out := map[string]any{}
	for k, vs := range honoQueries(r) {
		if len(vs) == 1 {
			out[k] = vs[0]
			continue
		}
		list := make([]any, len(vs))
		for i, v := range vs {
			list[i] = v
		}
		out[k] = list
	}
	return out
}

// jsonContentType is hono/validator's test for a JSON body (case-sensitive, as there).
var jsonContentType = regexp.MustCompile(`^application/([a-z\-.]+\+)?json(;` + wsClass + `*[a-zA-Z0-9\-]+=([^;]+))*$`)

// openapiJSON is the body the json validator handed zod: the parsed JSON when
// the request says it is JSON, else {}. ok is false when it says so and isn't:
// the validator answered that with malformedJSON.
func openapiJSON(r *http.Request) (v any, ok bool) {
	if !jsonContentType.MatchString(headerValue(r, "Content-Type")) {
		return map[string]any{}, true
	}
	v, err := web.JSONBody(r)
	if err != nil {
		return nil, false
	}
	return v, true
}

// malformedJSON is the HTTPException the json validator threw, as Hono's
// default error handler answered it.
func malformedJSON(w http.ResponseWriter) error {
	return web.Text(w, http.StatusBadRequest, "text/plain;charset=UTF-8", "Malformed JSON in request body")
}

// zodPath is an issue's path: the keys from the root.
func zodPath(keys ...any) []any { return append([]any{}, keys...) }

// invalidType is zod's invalid_type issue for v where expected was wanted
// (zodType, in lists.go, names what it got).
func invalidType(path []any, expected string, v any) web.Issue {
	got := zodType(v)
	return web.Issue{Code: "invalid_type", Expected: expected, Received: got, Path: path, Message: "Expected " + expected + ", received " + got}
}

// zodJoin is zod's util.joinValues: strings quoted, joined by " | ".
func zodJoin(options []string) string {
	quoted := make([]string, len(options))
	for i, o := range options {
		quoted[i] = "'" + o + "'"
	}
	return strings.Join(quoted, " | ")
}

// checkEnum is z.enum(options) on v: a string that isn't one of them is
// invalid_enum_value, anything else invalid_type. ZodEnum built both issues
// in its own key order.
func checkEnum(path []any, v any, options []string) *web.Issue {
	s, ok := v.(string)
	if !ok {
		issue := invalidType(path, zodJoin(options), v)
		issue.Keys = []string{"expected", "received", "code", "path", "message"}
		return &issue
	}
	if indexOf(options, s) >= 0 {
		return nil
	}
	return &web.Issue{Code: "invalid_enum_value", Received: s, Path: path, Options: options,
		Message: "Invalid enum value. Expected " + zodJoin(options) + ", received '" + s + "'",
		Keys:    []string{"received", "code", "options", "path", "message"}}
}

// checkType is a z.string() / z.boolean() check: nil when v has that type.
func checkType(path []any, v any, expected string) *web.Issue {
	if zodType(v) == expected {
		return nil
	}
	issue := invalidType(path, expected, v)
	return &issue
}
