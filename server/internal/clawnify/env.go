// Package clawnify ports the Clawnify platform clients the TS code used
// through @clawnify/queue, @clawnify/services and @clawnify/connections.
package clawnify

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
)

// Env is the platform configuration the Worker passes to the container. In
// production Clawnify injects the org's token and id; the URLs are only set
// off-platform, to point the clients at a local stand-in.
type Env struct {
	Token          string // CLAWNIFY_TOKEN
	OrgID          string // CLAWNIFY_ORG_ID
	APIURL         string // CLAWNIFY_API_URL
	QueueURL       string // CLAWNIFY_QUEUE_URL
	ServicesURL    string // CLAWNIFY_SERVICES_URL
	ConnectionsURL string // CLAWNIFY_CONNECTIONS_URL: the Worker's credentials bridge
	SlackChannel   string // SLACK_CHANNEL
}

// EnvFromOS reads the configuration from the process environment.
func EnvFromOS() Env {
	return Env{
		Token:          os.Getenv("CLAWNIFY_TOKEN"),
		OrgID:          os.Getenv("CLAWNIFY_ORG_ID"),
		APIURL:         os.Getenv("CLAWNIFY_API_URL"),
		QueueURL:       os.Getenv("CLAWNIFY_QUEUE_URL"),
		ServicesURL:    os.Getenv("CLAWNIFY_SERVICES_URL"),
		ConnectionsURL: os.Getenv("CLAWNIFY_CONNECTIONS_URL"),
		SlackChannel:   os.Getenv("SLACK_CHANNEL"),
	}
}

// trimSlashes is .replace(/\/+$/, "").
func trimSlashes(s string) string { return strings.TrimRight(s, "/") }

// doJSON sends one request and returns the status and the whole body.
func doJSON(ctx context.Context, hc *http.Client, method, url string, header map[string]string, body any) (int, []byte, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, rd)
	if err != nil {
		return 0, nil, err
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	res, err := hc.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	return res.StatusCode, b, err
}
