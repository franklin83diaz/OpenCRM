// Package web is the thin layer between net/http and the ported routes: a
// router that fails the way Hono did, JSON in and out with JS's rules, and
// who is calling.
package web

import (
	"log"
	"net/http"
)

// HandlerFunc is a route. A returned error becomes a 500, as a throw did in Hono.
type HandlerFunc func(w http.ResponseWriter, r *http.Request) error

// Router dispatches on Go 1.22 patterns ("GET /api/companies/{id}").
// Unlike http.ServeMux it never answers 405 or redirects: anything that is not
// a registered route goes to the not-found handler, which is what Hono did.
type Router struct {
	mux      *http.ServeMux
	notFound HandlerFunc
	patterns []string
}

// NewRouter returns a router whose not-found answer is Hono's default, plain
// "404 Not Found", until NotFound replaces it.
func NewRouter() *Router {
	return &Router{
		mux: http.NewServeMux(),
		notFound: func(w http.ResponseWriter, r *http.Request) error {
			return Text(w, http.StatusNotFound, "text/plain; charset=UTF-8", "404 Not Found")
		},
	}
}

// Handle registers h for pattern. Like ServeMux, a "GET" pattern also serves HEAD.
func (rt *Router) Handle(pattern string, h HandlerFunc) {
	rt.mux.Handle(pattern, route(h))
	rt.patterns = append(rt.patterns, pattern)
}

// Patterns lists the registered routes, in the order Handle added them.
func (rt *Router) Patterns() []string { return append([]string(nil), rt.patterns...) }

// NotFound sets what unmatched requests get.
func (rt *Router) NotFound(h HandlerFunc) { rt.notFound = h }

func (rt *Router) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// For a wrong method ServeMux.Handler hands back its 405 handler with an
	// empty pattern, and for an uncleaned path ("//", "/x/..") or a missing
	// trailing slash a redirect; Hono answered all of those with its not-found.
	// So only a request that lands on a registered route goes through the mux,
	// which is also what sets PathValue.
	if h, _ := rt.mux.Handler(r); !isRoute(h) {
		run(w, r, rt.notFound)
		return
	}
	rt.mux.ServeHTTP(w, r)
}

// route marks the handlers Handle registered, as opposed to the mux's own.
type route HandlerFunc

func (h route) ServeHTTP(w http.ResponseWriter, r *http.Request) { run(w, r, HandlerFunc(h)) }

func isRoute(h http.Handler) bool {
	_, ok := h.(route)
	return ok
}

// run calls h and turns a returned error or a panic into Hono's default 500.
func run(w http.ResponseWriter, r *http.Request, h HandlerFunc) {
	tw := &trackingWriter{ResponseWriter: w}
	defer func() {
		if p := recover(); p != nil {
			if p == http.ErrAbortHandler {
				panic(p)
			}
			fail(tw, r, p)
		}
	}()
	if err := h(tw, r); err != nil {
		fail(tw, r, err)
	}
}

func fail(w *trackingWriter, r *http.Request, cause any) {
	log.Printf("%s %s: %v", r.Method, r.URL.Path, cause)
	if w.wrote {
		return // the response is already on its way; nothing left to say
	}
	Text(w, http.StatusInternalServerError, "text/plain; charset=UTF-8", "Internal Server Error")
}

// trackingWriter remembers whether the status line went out, so a late error
// doesn't try to write a second one.
type trackingWriter struct {
	http.ResponseWriter
	wrote bool
}

func (w *trackingWriter) WriteHeader(status int) {
	w.wrote = true
	w.ResponseWriter.WriteHeader(status)
}

func (w *trackingWriter) Write(b []byte) (int, error) {
	w.wrote = true
	return w.ResponseWriter.Write(b)
}

// Unwrap lets http.ResponseController reach the underlying writer (Flush etc).
func (w *trackingWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
