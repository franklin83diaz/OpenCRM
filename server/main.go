// The API server that runs in the Cloudflare Container. It reaches D1 through
// the Worker (D1_URL, set by the Api container class), and stops gracefully
// on SIGTERM, which is how the container is put to sleep. The Worker also
// passes the ADMIN_EMAIL and ADMIN_PASSWORD secrets, when set: the first
// admin of a CRM that has no accounts yet.
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"opencrm/internal/crm"
	"opencrm/internal/db"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	d1 := os.Getenv("D1_URL")
	if d1 == "" {
		log.Fatal("D1_URL is not set: the Worker passes it to the container (http://d1.internal)")
	}
	// The bridge's own transport: every query is a request to the Worker, and
	// the default keeps only 2 idle connections per host, so parallel queries
	// would keep dialing new ones. D1 gives up on a query after 30 s, so a
	// bridge call far past that is stuck.
	bridge := http.DefaultTransport.(*http.Transport).Clone()
	bridge.MaxIdleConnsPerHost = 32
	db.Use(db.NewD1(d1, &http.Client{Timeout: time.Minute, Transport: bridge}))

	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           crm.New(&crm.App{AdminEmail: os.Getenv("ADMIN_EMAIL"), AdminPassword: os.Getenv("ADMIN_PASSWORD")}),
		ReadHeaderTimeout: 10 * time.Second,
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()
	go func() {
		if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	}()
	log.Printf("listening on :%s", port)

	<-ctx.Done()
	shutdown, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdown); err != nil {
		log.Printf("shutdown: %v", err)
	}
}
