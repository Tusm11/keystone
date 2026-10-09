// keystone-origin: the authoritative shortener API.
// Edge Worker calls this service on cache miss, and for every write.
package main

import (
	"log"
	"net/http"
	"os"
	"time"

	keystonehttp "github.com/Tusm11/keystone/origin/internal/http"
	"github.com/Tusm11/keystone/origin/internal/storage"
)

func main() {
	addr := os.Getenv("ORIGIN_ADDR")
	if addr == "" {
		addr = ":8080"
	}

	store := storage.NewMemory()
	server := &keystonehttp.Server{Store: store}

	// Explicit timeouts on the http.Server — the Go stdlib default is "no
	// timeout", which is a well-known production footgun: a slow or malicious
	// client can hold a connection open indefinitely and exhaust fds.
	s := &http.Server{
		Addr:         addr,
		Handler:      server.Routes(),
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	log.Printf("keystone-origin listening on %s", addr)
	if err := s.ListenAndServe(); err != nil {
		log.Fatalf("server failed: %v", err)
	}
}
