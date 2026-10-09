// Package http wires routes to primitives + storage.
// Handlers are deliberately thin: parse → call primitive/store → write JSON.
package http

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Tusm11/keystone/origin/internal/events"
	"github.com/Tusm11/keystone/origin/internal/primitives/codegen"
	"github.com/Tusm11/keystone/origin/internal/storage"
)

type Server struct {
	Store  storage.Store
	Clicks *events.Publisher // optional; nil when Redis isn't wired
}

func (s *Server) Routes() http.Handler {
	r := chi.NewRouter()
	r.Get("/health", s.health)
	r.Post("/shorten", s.shorten)
	r.Get("/{code}", s.resolve)
	return r
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"status":  "ok",
		"service": "keystone-origin",
	})
}

type shortenRequest struct {
	URL string `json:"url"`
}

type shortenResponse struct {
	Code    string `json:"code"`
	LongURL string `json:"long_url"`
}

func (s *Server) shorten(w http.ResponseWriter, r *http.Request) {
	var req shortenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	if req.URL == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "url is required"})
		return
	}

	// Retry on rare collisions (random code already taken).
	const maxRetries = 5
	for i := 0; i < maxRetries; i++ {
		code, err := codegen.Random(7)
		if err != nil {
			slog.Error("codegen error", "err", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "codegen failed"})
			return
		}
		saveErr := s.Store.Save(code, req.URL)
		if saveErr == nil {
			writeJSON(w, http.StatusCreated, shortenResponse{Code: code, LongURL: req.URL})
			return
		}
		if !errors.Is(saveErr, storage.ErrCodeTaken) {
			slog.Error("store error", "err", saveErr)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "storage failed"})
			return
		}
		// collision — loop and generate a new code
	}
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "code collision after retries"})
}

type resolveResponse struct {
	Code    string `json:"code"`
	LongURL string `json:"long_url"`
}

func (s *Server) resolve(w http.ResponseWriter, r *http.Request) {
	code := chi.URLParam(r, "code")
	longURL, err := s.Store.Get(code)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "storage failed"})
		return
	}

	// Fire the click event into the pipeline. Short-timeout, non-blocking;
	// a dead analytics path must not break the redirect lookup.
	s.Clicks.PublishClick(code)

	writeJSON(w, http.StatusOK, resolveResponse{Code: code, LongURL: longURL})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
