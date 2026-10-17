// Package discovery serves what @clawnify/routes' mountDiscovery did: the
// OpenAPI document, the llms.txt index, and a not-found answer that points
// callers at both. The three files began as captures of the TS app's output
// (its OpenAPI document was generated from the zod route schemas, which Go
// doesn't have); now they are maintained by hand beside the Go routes, and
// crm's TestDiscoveryListsExactlyTheRoutes is the judge: llms.txt and
// endpoints.json must list exactly the routes the router serves, and the
// OpenAPI document (which covers only the routes TS described with zod
// schemas) only routes that exist. Adding or removing a route means editing
// them until that test passes.
package discovery

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"net/http"
	"strings"

	"opencrm/internal/web"
)

//go:embed openapi.json
var openapi string

//go:embed llms.txt
var llms string

//go:embed endpoints.json
var endpointsFile []byte

// endpoints is endpoints.json, compacted once: the list a signed-in caller
// gets with a 404.
var endpoints = func() json.RawMessage {
	var buf bytes.Buffer
	if err := json.Compact(&buf, endpointsFile); err != nil {
		panic("discovery: endpoints.json: " + err.Error())
	}
	return buf.Bytes()
}()

// Mount adds GET /api/openapi.json, GET /llms.txt and the not-found handler.
func Mount(rt *web.Router) {
	rt.Handle("GET /api/openapi.json", func(w http.ResponseWriter, r *http.Request) error {
		return web.Text(w, http.StatusOK, "application/json", openapi)
	})
	rt.Handle("GET /llms.txt", func(w http.ResponseWriter, r *http.Request) error {
		w.Header().Set("Cache-Control", "public, max-age=300")
		return web.Text(w, http.StatusOK, "text/markdown; charset=utf-8", llms)
	})
	rt.NotFound(notFound)
}

type notFoundBody struct {
	Error     string          `json:"error"`
	Message   string          `json:"message"`
	Hint      string          `json:"hint"`
	Endpoints json.RawMessage `json:"endpoints,omitempty"`
}

// notFound answers API paths in JSON a client or agent can act on, and
// everything else with plain text. Only a signed-in caller gets the endpoint
// list. Behind the auth middleware an anonymous /api request is answered 401
// before it gets here, so nobody probes the API's shape without an account.
func notFound(w http.ResponseWriter, r *http.Request) error {
	if !strings.HasPrefix(r.URL.Path, "/api") {
		return web.Text(w, http.StatusNotFound, "text/plain; charset=UTF-8", "404 Not Found")
	}
	body := notFoundBody{
		Error:   "not_found",
		Message: "No route for " + r.Method + " " + r.URL.Path + " on this app.",
		Hint:    "GET /llms.txt lists every endpoint; GET /api/openapi.json has full request/response schemas.",
	}
	if web.Caller(r) != "public" {
		body.Endpoints = endpoints
	}
	return web.JSON(w, http.StatusNotFound, body)
}
