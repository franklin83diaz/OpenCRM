package crm

// What Hono and @hono/zod-openapi did to a request before an OpenAPI route's
// handler ran: the query string as c.req.queries() read it, the JSON body as
// the json validator read it, and zod 3's issues for what failed. Routes
// declared with app.openapi(createRoute(…)) validate with these; plain
// app.get/app.post routes never did.

import (
	"errors"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

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

// requiredIssue is zod's invalid_type for a key the input lacks: zod read it
// as undefined, and its error map says "Required" for that.
func requiredIssue(path []any, expected string) web.Issue {
	return web.Issue{Code: "invalid_type", Expected: expected, Received: "undefined", Path: path, Message: "Required"}
}

// arrayTooSmall and arrayTooBig are .min(n) and .max(n) on a z.array. ZodArray
// built these with no message of its own, so makeIssue kept the message key
// where the (undefined) one was and put path after it.
func arrayTooSmall(path []any, min int) web.Issue {
	yes, no := true, false
	return web.Issue{Code: "too_small", Minimum: min, Type: "array", Inclusive: &yes, Exact: &no, Path: path,
		Message: "Array must contain at least " + strconv.Itoa(min) + " element(s)",
		Keys:    []string{"code", "minimum", "type", "inclusive", "exact", "message", "path"}}
}

func arrayTooBig(path []any, max int) web.Issue {
	yes, no := true, false
	return web.Issue{Code: "too_big", Maximum: max, Type: "array", Inclusive: &yes, Exact: &no, Path: path,
		Message: "Array must contain at most " + strconv.Itoa(max) + " element(s)",
		Keys:    []string{"code", "maximum", "type", "inclusive", "exact", "message", "path"}}
}

// stringTooSmall and stringTooBig are .min(n) and .max(n) on a z.string(),
// in ZodString's key order (the same as ZodArray's). JS counts the length in
// UTF-16 units (u16Len).
func stringTooSmall(path []any, min int) web.Issue {
	yes, no := true, false
	return web.Issue{Code: "too_small", Minimum: min, Type: "string", Inclusive: &yes, Exact: &no, Path: path,
		Message: "String must contain at least " + strconv.Itoa(min) + " character(s)",
		Keys:    []string{"code", "minimum", "type", "inclusive", "exact", "message", "path"}}
}

func stringTooBig(path []any, max int) web.Issue {
	yes, no := true, false
	return web.Issue{Code: "too_big", Maximum: max, Type: "string", Inclusive: &yes, Exact: &no, Path: path,
		Message: "String must contain at most " + strconv.Itoa(max) + " character(s)",
		Keys:    []string{"code", "maximum", "type", "inclusive", "exact", "message", "path"}}
}

// stringField is one key of a z.object body schema whose value is a
// z.string(): .min(min) when min > 0, .nullable() and/or .optional().
type stringField struct {
	key      string
	min      int
	nullable bool
	optional bool
}

// stringFieldIssues checks body against fields, in the schema's key order: a
// missing required key is "Required", a value of another type invalid_type,
// a short string too_small. Keys the schema doesn't name raise nothing
// (.passthrough() keeps them, a plain z.object drops them).
func stringFieldIssues(body map[string]any, fields []stringField) []web.Issue {
	var issues []web.Issue
	for _, f := range fields {
		v, present := body[f.key]
		switch {
		case !present:
			if !f.optional {
				issues = append(issues, requiredIssue(zodPath(f.key), "string"))
			}
		case v == nil && f.nullable:
		default:
			s, isString := v.(string)
			if !isString {
				issues = append(issues, invalidType(zodPath(f.key), "string", v))
			} else if f.min > 0 && u16Len(s) < f.min {
				issues = append(issues, stringTooSmall(zodPath(f.key), f.min))
			}
		}
	}
	return issues
}

// optionalStrings is a query schema of optional z.string() fields, checked in
// the schema's order: a parameter sent twice arrives as a list.
func optionalStrings(q map[string]any, keys ...string) []web.Issue {
	var issues []web.Issue
	for _, key := range keys {
		if v, present := q[key]; present {
			if issue := checkType(zodPath(key), v, "string"); issue != nil {
				issues = append(issues, *issue)
			}
		}
	}
	return issues
}

// openapiJSONType is @hono/zod-openapi's own test of the Content-Type (a
// prefix, case-sensitive), made before the json validator's.
var openapiJSONType = regexp.MustCompile(`^application/([a-z\-.]+\+)?json`)

// optionalJSONBody is the body of an OpenAPI route whose body isn't marked
// required: zod-openapi ran the json validator only when the request had a
// Content-Type passing openapiJSONType, and otherwise handed the handler {}
// unchecked (checked false). ok is false for malformed JSON (malformedJSON).
func optionalJSONBody(r *http.Request) (v any, checked, ok bool) {
	ct := headerValue(r, "Content-Type")
	if ct == "" || !openapiJSONType.MatchString(ct) {
		return map[string]any{}, false, true
	}
	v, ok = openapiJSON(r)
	return v, true, ok
}

// ── zod schemas as checks ──────────────────────────────────────────
// A zodCheck is one zod 3 schema applied to one value: the issues it raised,
// none when the value passes. present is false for a key the input lacks
// (zod read undefined). They compose as the schemas did, so a route's body or
// query schema reads like its createRoute: zObject(zKey("label", zString(1,
// 0)), zKey("position", zOptional(zInt())), …).

type zodCheck func(path []any, v any, present bool) []web.Issue

// zodKey is one key of a z.object shape.
type zodKey struct {
	key   string
	check zodCheck
}

func zKey(key string, check zodCheck) zodKey { return zodKey{key, check} }

// pathAt is path extended by key, without sharing path's backing array.
func pathAt(path []any, key any) []any {
	return append(append(make([]any, 0, len(path)+1), path...), key)
}

// zObject is z.object(shape) (stripping or passthrough alike: unknown keys
// raise nothing): a non-object is invalid_type, else each key in the shape's
// order.
func zObject(keys ...zodKey) zodCheck {
	return func(path []any, v any, present bool) []web.Issue {
		if !present {
			return []web.Issue{requiredIssue(path, "object")}
		}
		obj, ok := v.(map[string]any)
		if !ok {
			return []web.Issue{invalidType(path, "object", v)}
		}
		var issues []web.Issue
		for _, k := range keys {
			val, has := obj[k.key]
			issues = append(issues, k.check(pathAt(path, k.key), val, has)...)
		}
		return issues
	}
}

// zOptional is .optional(): an absent key passes.
func zOptional(c zodCheck) zodCheck {
	return func(path []any, v any, present bool) []web.Issue {
		if !present {
			return nil
		}
		return c(path, v, present)
	}
}

// zNullable is .nullable(): null passes.
func zNullable(c zodCheck) zodCheck {
	return func(path []any, v any, present bool) []web.Issue {
		if present && v == nil {
			return nil
		}
		return c(path, v, present)
	}
}

// zString is z.string(), with .min(min) and .max(max) when they are above 0
// (lengths in UTF-16 units, as JS counts them).
func zString(min, max int) zodCheck {
	return func(path []any, v any, present bool) []web.Issue {
		if !present {
			return []web.Issue{requiredIssue(path, "string")}
		}
		s, ok := v.(string)
		if !ok {
			return []web.Issue{invalidType(path, "string", v)}
		}
		var issues []web.Issue
		if n := u16Len(s); min > 0 && n < min {
			issues = append(issues, stringTooSmall(path, min))
		} else if max > 0 && n > max {
			issues = append(issues, stringTooBig(path, max))
		}
		return issues
	}
}

// zBool is z.boolean().
func zBool() zodCheck {
	return func(path []any, v any, present bool) []web.Issue {
		if !present {
			return []web.Issue{requiredIssue(path, "boolean")}
		}
		if issue := checkType(path, v, "boolean"); issue != nil {
			return []web.Issue{*issue}
		}
		return nil
	}
}

// zInt is z.number().int(). Its int check is an invalid_type of its own,
// built with an undefined message, so makeIssue kept message before path.
func zInt() zodCheck {
	return func(path []any, v any, present bool) []web.Issue {
		if !present {
			return []web.Issue{requiredIssue(path, "number")}
		}
		f, ok := v.(float64)
		if !ok {
			return []web.Issue{invalidType(path, "number", v)}
		}
		if math.IsInf(f, 0) || f != math.Trunc(f) {
			return []web.Issue{{Code: "invalid_type", Expected: "integer", Received: "float", Path: path,
				Message: "Expected integer, received float", Keys: []string{"code", "expected", "received", "message", "path"}}}
		}
		return nil
	}
}

// zEnum is z.enum(options). ZodEnum reads a missing key as any other
// non-string, in its own key order; zod's error map says "Required" for it.
func zEnum(options ...string) zodCheck {
	return func(path []any, v any, present bool) []web.Issue {
		if !present {
			return []web.Issue{{Code: "invalid_type", Expected: zodJoin(options), Received: "undefined", Path: path,
				Message: "Required", Keys: []string{"expected", "received", "code", "path", "message"}}}
		}
		if issue := checkEnum(path, v, options); issue != nil {
			return []web.Issue{*issue}
		}
		return nil
	}
}

// zArray is z.array(item) with .max(max) when above 0: the length first,
// then each item.
func zArray(item zodCheck, max int) zodCheck {
	return func(path []any, v any, present bool) []web.Issue {
		if !present {
			return []web.Issue{requiredIssue(path, "array")}
		}
		list, ok := v.([]any)
		if !ok {
			return []web.Issue{invalidType(path, "array", v)}
		}
		var issues []web.Issue
		if max > 0 && len(list) > max {
			issues = append(issues, arrayTooBig(path, max))
		}
		for i, x := range list {
			issues = append(issues, item(pathAt(path, i), x, true)...)
		}
		return issues
	}
}

// zNumberOrString is z.union([z.number(), z.string()]): when neither takes
// the value, one invalid_union carrying each option's ZodError.
func zNumberOrString() zodCheck {
	return func(path []any, v any, present bool) []web.Issue {
		switch v.(type) {
		case float64, string:
			if present {
				return nil
			}
		}
		option := func(expected string) web.UnionError {
			issue := invalidType(path, expected, v)
			if !present {
				issue = requiredIssue(path, expected)
			}
			return web.UnionError{Issues: []web.Issue{issue}, Name: "ZodError"}
		}
		return []web.Issue{{Code: "invalid_union", UnionErrors: []web.UnionError{option("number"), option("string")}, Path: path,
			Message: "Invalid input", Keys: []string{"code", "unionErrors", "path", "message"}}}
	}
}

// zCustomValues is CustomValues, z.record(z.string(), z.any()).optional():
// absent or an object passes (see CustomValuesIssue).
func zCustomValues() zodCheck {
	return func(path []any, v any, present bool) []web.Issue {
		if _, isObject := v.(map[string]any); !present || isObject {
			return nil
		}
		return []web.Issue{invalidType(path, "object", v)}
	}
}

// zodIssues runs a route's root schema on a request's body or query.
// @hono/zod-validator parsed with safeParseAsync, where a z.union settles its
// options as promises: its invalid_union lands after every issue the
// synchronous schemas raised, whatever its place in the shape. Unions are the
// only asynchronous schemas here, so they go last, in their own order.
func zodIssues(schema zodCheck, v any) []web.Issue {
	var issues, unions []web.Issue
	for _, issue := range schema(zodPath(), v, true) {
		if issue.Code == "invalid_union" {
			unions = append(unions, issue)
		} else {
			issues = append(issues, issue)
		}
	}
	return append(issues, unions...)
}

// errURIMalformed is the URIError decodeURIComponent threw.
var errURIMalformed = errors.New("URI malformed")

// decodeURIComponent is JS's: each %XX escape decoded; a multi-byte character
// must be spelled as escapes that form valid UTF-8, else "URI malformed".
func decodeURIComponent(s string) (string, error) {
	if !strings.Contains(s, "%") {
		return s, nil
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] != '%' {
			b.WriteByte(s[i])
			i++
			continue
		}
		c, ok := escapedByte(s, i)
		if !ok {
			return "", errURIMalformed
		}
		i += 3
		if c < 0x80 {
			b.WriteByte(c)
			continue
		}
		var n int
		switch {
		case c&0xE0 == 0xC0:
			n = 1
		case c&0xF0 == 0xE0:
			n = 2
		case c&0xF8 == 0xF0:
			n = 3
		default:
			return "", errURIMalformed
		}
		seq := []byte{c}
		for j := 0; j < n; j++ {
			cc, ok := escapedByte(s, i)
			if !ok || cc&0xC0 != 0x80 {
				return "", errURIMalformed
			}
			seq = append(seq, cc)
			i += 3
		}
		if !utf8.Valid(seq) { // overlong forms, surrogates, past U+10FFFF
			return "", errURIMalformed
		}
		b.Write(seq)
	}
	return b.String(), nil
}

// escapedByte reads the %XX escape at s[i].
func escapedByte(s string, i int) (byte, bool) {
	if i+2 >= len(s) || s[i] != '%' {
		return 0, false
	}
	v, err := strconv.ParseUint(s[i+1:i+3], 16, 8)
	if err != nil {
		return 0, false
	}
	return byte(v), true
}
