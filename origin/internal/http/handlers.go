// Package http wires routes to primitives + storage.
// Handlers are deliberately thin: parse → call primitive/store → write JSON.
package http

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Tusm11/keystone/origin/internal/events"
	"github.com/Tusm11/keystone/origin/internal/primitives/capability"
	"github.com/Tusm11/keystone/origin/internal/primitives/codegen"
	"github.com/Tusm11/keystone/origin/internal/signing"
	"github.com/Tusm11/keystone/origin/internal/storage"
)

// hostname is captured once at startup; used in /health responses so we
// can see which replica served a request behind the load balancer.
var hostname = func() string {
	h, err := os.Hostname()
	if err != nil {
		return "unknown"
	}
	return h
}()

type Server struct {
	Store   storage.Store
	Clicks  *events.Publisher
	Signers *signing.Registry
	Uses    *signing.UsesCounter
}

func (s *Server) Routes() http.Handler {
	r := chi.NewRouter()
	r.Get("/health", s.health)
	r.Post("/shorten", s.shorten)
	r.Post("/capabilities", s.mintCapability)
	r.Get("/{code}", s.resolve)
	return r
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":          "ok",
		"service":         "keystone-origin",
		"host":            hostname,
		"capabilities_on": s.Signers != nil && s.Signers.HasLocalSigner(),
	})
}

// --- shorten ---------------------------------------------------------------

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
		writeJSON(w, http.StatusBadRequest, errJSON("invalid json"))
		return
	}
	if req.URL == "" {
		writeJSON(w, http.StatusBadRequest, errJSON("url is required"))
		return
	}

	const maxRetries = 5
	for i := 0; i < maxRetries; i++ {
		code, err := codegen.Random(7)
		if err != nil {
			slog.Error("codegen error", "err", err)
			writeJSON(w, http.StatusInternalServerError, errJSON("codegen failed"))
			return
		}
		saveErr := s.Store.Save(code, req.URL)
		if saveErr == nil {
			writeJSON(w, http.StatusCreated, shortenResponse{Code: code, LongURL: req.URL})
			return
		}
		if !errors.Is(saveErr, storage.ErrCodeTaken) {
			slog.Error("store error", "err", saveErr)
			writeJSON(w, http.StatusInternalServerError, errJSON("storage failed"))
			return
		}
	}
	writeJSON(w, http.StatusInternalServerError, errJSON("code collision after retries"))
}

// --- capabilities ----------------------------------------------------------

type mintRequest struct {
	Code         string `json:"code"`
	Scope        string `json:"scope"`
	ExpiresInSec int64  `json:"expires_in_sec"`
	MaxUses      int64  `json:"max_uses"`
}

type mintResponse struct {
	Code    string `json:"code"`
	Token   string `json:"token"`
	ExpAt   int64  `json:"exp,omitempty"`
	MaxUses int64  `json:"max_uses,omitempty"`
	Signer  string `json:"signer"`
}

func (s *Server) mintCapability(w http.ResponseWriter, r *http.Request) {
	if s.Signers == nil || !s.Signers.HasLocalSigner() {
		writeJSON(w, http.StatusServiceUnavailable, errJSON("signing not configured on this node"))
		return
	}
	var req mintRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errJSON("invalid json"))
		return
	}
	if req.Code == "" {
		writeJSON(w, http.StatusBadRequest, errJSON("code is required"))
		return
	}
	if _, err := s.Store.Get(req.Code); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, errJSON("code not found"))
			return
		}
		writeJSON(w, http.StatusInternalServerError, errJSON("storage failed"))
		return
	}
	scope := req.Scope
	if scope == "" {
		scope = "read-only"
	}
	signerID, priv, _ := s.Signers.LocalSigner()
	cap, err := capability.New(req.Code, scope, signerID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errJSON(err.Error()))
		return
	}
	if req.ExpiresInSec > 0 {
		cap.Exp = time.Now().Add(time.Duration(req.ExpiresInSec) * time.Second).Unix()
	}
	cap.Uses = req.MaxUses

	token, err := cap.Sign(priv)
	if err != nil {
		slog.Error("sign failed", "err", err)
		writeJSON(w, http.StatusInternalServerError, errJSON("sign failed"))
		return
	}
	writeJSON(w, http.StatusCreated, mintResponse{
		Code:    cap.Code,
		Token:   token,
		ExpAt:   cap.Exp,
		MaxUses: cap.Uses,
		Signer:  signerID,
	})
}

// --- resolve ---------------------------------------------------------------

type resolveResponse struct {
	Code    string `json:"code"`
	LongURL string `json:"long_url"`
}

func (s *Server) resolve(w http.ResponseWriter, r *http.Request) {
	code := chi.URLParam(r, "code")

	if token := r.URL.Query().Get("k"); token != "" {
		if err := s.verifyCapability(token, code); err != nil {
			slog.Info("capability rejected", "code", code, "err", err)
			writeJSON(w, http.StatusForbidden, errJSON(err.Error()))
			return
		}
	}

	longURL, err := s.Store.Get(code)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, errJSON("not found"))
			return
		}
		writeJSON(w, http.StatusInternalServerError, errJSON("storage failed"))
		return
	}

	s.Clicks.PublishClick(code)
	// Add the serving replica's hostname to the response so load-balancing
	// is observable from the client side under a curl loop.
	w.Header().Set("X-Served-By", hostname)
	writeJSON(w, http.StatusOK, resolveResponse{Code: code, LongURL: longURL})
}

func (s *Server) verifyCapability(token, code string) error {
	peeked, err := peekSigner(token)
	if err != nil {
		return err
	}
	pub, err := s.Signers.PublicKey(peeked)
	if err != nil {
		return err
	}
	cap, err := capability.Verify(token, pub, time.Now())
	if err != nil {
		return err
	}
	if cap.Code != code {
		return errors.New("capability code does not match url")
	}
	if err := s.Uses.Charge(cap.Nonce, cap.Uses); err != nil {
		return err
	}
	return nil
}

func peekSigner(token string) (string, error) {
	parts := splitN(token, '.', 3)
	if len(parts) != 3 || parts[0] != capability.Version {
		return "", capability.ErrMalformed
	}
	body, err := base64RawURLDecode(parts[1])
	if err != nil {
		return "", capability.ErrMalformed
	}
	var peek struct {
		Signer string `json:"signer"`
	}
	if err := json.Unmarshal(body, &peek); err != nil || peek.Signer == "" {
		return "", capability.ErrMalformed
	}
	return peek.Signer, nil
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func errJSON(msg string) map[string]string {
	return map[string]string{"error": msg}
}
