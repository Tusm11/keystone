// Package http wires routes to primitives + storage.
// Handlers are deliberately thin: parse → call primitive/store → write JSON.
package http

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Tusm11/keystone/origin/internal/events"
	"github.com/Tusm11/keystone/origin/internal/primitives/capability"
	"github.com/Tusm11/keystone/origin/internal/primitives/codegen"
	"github.com/Tusm11/keystone/origin/internal/signing"
	"github.com/Tusm11/keystone/origin/internal/storage"
)

type Server struct {
	Store    storage.Store
	Clicks   *events.Publisher   // optional; nil without Redis
	Signers  *signing.Registry   // required; may be verifier-only
	Uses     *signing.UsesCounter // optional; nil without Redis
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
	Code          string `json:"code"`
	Scope         string `json:"scope"`           // defaults to "read-only"
	ExpiresInSec  int64  `json:"expires_in_sec"`  // 0 = no expiry
	MaxUses       int64  `json:"max_uses"`        // 0 = unlimited
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
	// Confirm the code exists — we won't mint for mappings that don't resolve.
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

	// Capability verification (optional, present when ?k= is passed).
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
	writeJSON(w, http.StatusOK, resolveResponse{Code: code, LongURL: longURL})
}

// verifyCapability runs the full verification pipeline for a presented token.
// Returns nil on success, a reason error on any failure. Called inline in
// resolve rather than as middleware so the error can be shaped per-case.
func (s *Server) verifyCapability(token, code string) error {
	// Peek at signer id without trusting it — Verify will re-check the sig.
	// We need the id to pick a public key. Two-pass pattern, documented in
	// docs/capability-spec.md §Verification.
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
	// Max-uses enforcement — Redis-backed atomic increment.
	if err := s.Uses.Charge(cap.Nonce, cap.Uses); err != nil {
		return err
	}
	return nil
}

// --- helpers ---------------------------------------------------------------

// peekSigner extracts the signer field from the token body WITHOUT
// treating it as trusted — only to look up a public key. The real
// signature check happens in Verify.
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
