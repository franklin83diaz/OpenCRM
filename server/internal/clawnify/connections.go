package clawnify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

// DefaultAPIBase is where the org's connections are brokered off-platform.
const DefaultAPIBase = "https://provision.clawnify.com"

// Connections reaches the org's connected services through a broker that
// holds the credentials, so this process never does:
//
//   - On the platform the Worker has the CREDENTIALS binding, and a container
//     cannot see bindings, so the Worker answers http://credentials.internal
//     (Env.ConnectionsURL) with the same three endpoints the remote API has.
//     That binding brokers only for a known org, so it needs Env.OrgID.
//   - Off it, CLAWNIFY_TOKEN talks to the remote API (Env.APIURL) itself.
//
// The bridge wins when both are set, as the binding did in TS. With neither,
// nothing reads as connected.
type Connections struct {
	env Env
	hc  *http.Client
}

// NewConnections builds the client; a nil hc is http.DefaultClient.
func NewConnections(env Env, hc *http.Client) *Connections {
	if hc == nil {
		hc = http.DefaultClient
	}
	return &Connections{env: env, hc: hc}
}

// broker is resolveBinding plus the canBroker rule: the base URL and headers
// of the endpoint to call, or false when nothing can broker.
func (c *Connections) broker() (base string, header map[string]string, ok bool) {
	header = map[string]string{"Content-Type": "application/json"}
	if c.env.ConnectionsURL != "" {
		return trimSlashes(c.env.ConnectionsURL), header, c.env.OrgID != ""
	}
	if c.env.Token != "" {
		base = c.env.APIURL
		if base == "" {
			base = DefaultAPIBase
		}
		header["Authorization"] = "Bearer " + c.env.Token
		if c.env.OrgID != "" {
			header["x-org-id"] = c.env.OrgID
		}
		return trimSlashes(base), header, true
	}
	return "", nil, false
}

// Token is the service's credential, or "" when there is none or the broker
// failed: a missing credential and a broken broker read the same.
func (c *Connections) Token(ctx context.Context, service string) string {
	base, header, ok := c.broker()
	if !ok {
		return ""
	}
	status, raw, err := doJSON(ctx, c.hc, "POST", base+"/v1/connections/token", header, map[string]any{"service": service})
	if err != nil || status < 200 || status > 299 {
		return ""
	}
	var j struct {
		Token string `json:"token"`
	}
	if json.Unmarshal(raw, &j) != nil {
		return ""
	}
	return j.Token
}

// Connected lists the services the org has connected; nil when nothing can
// broker or the call fails.
func (c *Connections) Connected(ctx context.Context) []string {
	base, header, ok := c.broker()
	if !ok {
		return nil
	}
	status, raw, err := doJSON(ctx, c.hc, "GET", base+"/v1/connections/connected", header, nil)
	if err != nil || status < 200 || status > 299 {
		return nil
	}
	var j struct {
		Services []string `json:"services"`
	}
	if json.Unmarshal(raw, &j) != nil {
		return nil
	}
	return j.Services
}

// Run executes a managed action, for example Run(ctx, "gmail", "GMAIL_FETCH_EMAILS", args),
// and returns its data. An unsuccessful action is an error carrying the
// broker's message.
func (c *Connections) Run(ctx context.Context, service, action string, args map[string]any) (any, error) {
	base, header, ok := c.broker()
	if !ok {
		return nil, fmt.Errorf(`"%s" needs the Clawnify credentials binding or CLAWNIFY_TOKEN`, service)
	}
	if args == nil {
		args = map[string]any{}
	}
	status, raw, err := doJSON(ctx, c.hc, "POST", base+"/v1/connections/execute", header,
		map[string]any{"service": service, "action": action, "args": args})
	if err != nil {
		return nil, err
	}
	var j struct {
		Data       any   `json:"data"`
		Error      any   `json:"error"`
		Successful *bool `json:"successful"`
	}
	_ = json.Unmarshal(raw, &j) // a body that is not JSON reads as {}
	if status < 200 || status > 299 {
		return nil, errors.New(errText(j.Error, fmt.Sprintf("connections %d", status)))
	}
	if j.Successful != nil && !*j.Successful {
		// The client words a broker's error as errText(error, "failed"); Run
		// words what is left as its own fallback.
		var msg any
		if j.Error != nil {
			msg = errText(j.Error, "failed")
		}
		return nil, errors.New(errText(msg, fmt.Sprintf(`%s action "%s" failed`, service, action)))
	}
	return j.Data, nil
}

// errText turns whatever an error field holds into a message.
func errText(e any, fallback string) string {
	if s, ok := e.(string); ok && s != "" && s != "[object Object]" {
		return s
	}
	if e == nil {
		return fallback
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if enc.Encode(e) != nil {
		return fallback
	}
	s := strings.TrimSuffix(buf.String(), "\n")
	if s == "" || s == "{}" {
		return fallback
	}
	return truncate(s, 500)
}

// truncate is s.slice(0, n), counting UTF-16 units as JS does.
func truncate(s string, n int) string {
	units := 0
	for i, r := range s {
		w := 1
		if r > 0xFFFF {
			w = 2
		}
		if units+w > n {
			return s[:i]
		}
		units += w
	}
	return s
}

// proxyConfig is a registry entry of @clawnify/integrations, for the services
// reached by direct API call. Only Granola is used.
type proxyConfig struct {
	authMode string
	baseURL  string
	headers  map[string]string
}

var proxyRegistry = map[string]proxyConfig{
	"granola": {
		authMode: "API_KEY",
		baseURL:  "https://public-api.granola.ai",
		headers:  map[string]string{"authorization": "Bearer ${apiKey}"},
	},
}

var templateRe = regexp.MustCompile(`\$\{([^}]+)\}`)

func fillTemplate(tpl, token, where string) (string, error) {
	var err error
	out := templateRe.ReplaceAllStringFunc(tpl, func(m string) string {
		key := strings.TrimSpace(m[2 : len(m)-1])
		if key == "accessToken" || key == "apiKey" {
			return token
		}
		err = fmt.Errorf("%s: unsupported template variable ${%s}", where, key)
		return ""
	})
	return out, err
}

// resolveRequest builds the URL and headers of a direct call: the registry's
// base and header templates filled with the credential. Pure, no network.
func resolveRequest(service string, cfg proxyConfig, token, endpoint string, query map[string]string) (string, map[string]string, error) {
	if cfg.baseURL == "" {
		return "", nil, fmt.Errorf(`"%s" has no base URL in the connections registry`, service)
	}
	base, err := fillTemplate(cfg.baseURL, token, service+" base URL")
	if err != nil {
		return "", nil, err
	}
	path := endpoint
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	u := trimSlashes(base) + path
	if len(query) > 0 {
		qs := url.Values{}
		for k, v := range query {
			qs.Set(k, v)
		}
		sep := "?"
		if strings.Contains(u, "?") {
			sep = "&"
		}
		u += sep + qs.Encode()
	}
	headers := map[string]string{}
	for name, tpl := range cfg.headers {
		v, err := fillTemplate(tpl, token, fmt.Sprintf(`%s header "%s"`, service, name))
		if err != nil {
			return "", nil, err
		}
		headers[name] = v
	}
	return u, headers, nil
}

// parseResponse reads a body as JSON when it says or looks like it, else as text.
func parseResponse(contentType string, body []byte) any {
	text := string(body)
	if text == "" {
		return nil
	}
	trimmed := strings.TrimLeft(text, " \t\r\n")
	looksJSON := strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[")
	if strings.Contains(contentType, "application/json") || strings.Contains(contentType, "+json") || looksJSON {
		var v any
		if json.Unmarshal(body, &v) == nil {
			return v
		}
	}
	return text
}

// Get calls a connected service's own API with its credential: GET endpoint,
// the registry's base URL, the query string, and the parsed answer. A non-2xx
// status is an error with the start of the answer.
func (c *Connections) Get(ctx context.Context, service, endpoint string, query map[string]string) (any, error) {
	cfg, ok := proxyRegistry[service]
	if !ok {
		return nil, fmt.Errorf(`"%s" is not a registry-backed own-tier provider; the generic proxy can't reach it. Use connect("%s").run(action, body) (find the action via searchActions).`, service, service)
	}
	token := c.Token(ctx, service)
	if token == "" {
		return nil, fmt.Errorf(`"%s" has no usable credential for a direct API call. If it's a managed/Composio integration, use connect("%s").run(action, body) (find the action via searchActions).`, service, service)
	}
	u, headers, err := resolveRequest(service, cfg, token, endpoint, query)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := c.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(res.Body); err != nil {
		return nil, err
	}
	data := parseResponse(res.Header.Get("Content-Type"), buf.Bytes())
	if res.StatusCode < 200 || res.StatusCode > 299 {
		snippet, isText := data.(string)
		if !isText {
			b, _ := json.Marshal(data)
			snippet = string(b)
		}
		return nil, fmt.Errorf("%s GET %s → %d: %s", service, endpoint, res.StatusCode, truncate(snippet, 300))
	}
	return data, nil
}
