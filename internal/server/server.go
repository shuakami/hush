// Package server hosts the HTTP API that CLI / SDK / MCP clients consume.
//
// All requests must carry a Bearer API key. The middleware resolves it via
// apikey.Manager, attaches the resolved Key to the request context, and
// requires per-route scopes.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/shuakami/hush/internal/apikey"
	"github.com/shuakami/hush/internal/audit"
	"github.com/shuakami/hush/internal/broker"
	"github.com/shuakami/hush/internal/inventory"
	"github.com/shuakami/hush/internal/vault"
)

// Server holds the HTTP handler and its dependencies.
type Server struct {
	Broker  *broker.Broker
	APIKeys *apikey.Manager
	Audit   *audit.Logger
	Listen  string
	mux     http.Handler
}

// New wires up routes and middleware.
func New(br *broker.Broker, akm *apikey.Manager, alog *audit.Logger, listen string) *Server {
	s := &Server{Broker: br, APIKeys: akm, Audit: alog, Listen: listen}

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(5 * time.Minute))

	r.Get("/healthz", s.healthz)
	r.Get("/version", s.version)

	r.Route("/api/v1", func(r chi.Router) {
		r.Use(s.authMiddleware)

		r.Get("/auth/whoami", s.whoami)

		r.Route("/secrets", func(r chi.Router) {
			r.With(requireScope("secret:list")).Get("/", s.listSecrets)
			r.With(requireScope("secret:read")).Get("/{name}", s.getSecret)
			r.With(requireScope("secret:list")).Head("/{name}", s.headSecret)
			r.With(requireScope("secret:write")).Put("/{name}", s.putSecret)
			r.With(requireScope("secret:write")).Delete("/{name}", s.deleteSecret)
		})

		r.Route("/hosts", func(r chi.Router) {
			r.With(requireScope("host:list")).Get("/", s.listHosts)
			r.With(requireScope("host:list")).Get("/{name}", s.getHost)
			r.With(requireScope("host:write")).Put("/{name}", s.putHost)
			r.With(requireScope("host:write")).Delete("/{name}", s.deleteHost)
		})

		r.With(requireScope("host:exec")).Post("/exec", s.exec)
		r.With(requireScope("host:exec")).Post("/exec/multi", s.execMulti)

		r.With(requireScope("host:put")).Post("/files/put", s.filePut)
		r.With(requireScope("host:get")).Post("/files/get", s.fileGet)

		r.With(requireScope("audit:read")).Get("/audit", s.tailAudit)
		r.With(requireScope("audit:read")).Get("/audit/verify", s.verifyAudit)

		r.Route("/api-keys", func(r chi.Router) {
			r.With(requireScope("apikey:write")).Post("/", s.mintAPIKey)
			r.With(requireScope("apikey:list")).Get("/", s.listAPIKeys)
			r.With(requireScope("apikey:write")).Delete("/{id}", s.revokeAPIKey)
		})
	})

	s.mux = r
	return s
}

// Handler returns the http.Handler for tests / embedded use.
func (s *Server) Handler() http.Handler { return s.mux }

// Serve blocks listening on the configured address.
func (s *Server) Serve() error {
	srv := &http.Server{
		Addr:              s.Listen,
		Handler:           s.mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	return srv.ListenAndServe()
}

// ---- middleware ------------------------------------------------------------

type ctxKey int

const (
	ctxKeyAPIKey ctxKey = iota + 1
	ctxKeyScope
)

func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ah := r.Header.Get("Authorization")
		if !strings.HasPrefix(ah, "Bearer ") {
			writeError(w, http.StatusUnauthorized, "missing bearer token")
			return
		}
		raw := strings.TrimPrefix(ah, "Bearer ")
		key, err := s.APIKeys.Verify(r.Context(), raw)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "invalid api key")
			return
		}
		ctx := context.WithValue(r.Context(), ctxKeyAPIKey, key)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func requireScope(scope string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			k := apiKeyFrom(r)
			if k == nil {
				writeError(w, http.StatusUnauthorized, "no api key in context")
				return
			}
			if !k.HasScope(scope) {
				writeError(w, http.StatusForbidden, fmt.Sprintf("scope %q required", scope))
				return
			}
			ctx := context.WithValue(r.Context(), ctxKeyScope, scope)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func apiKeyFrom(r *http.Request) *apikey.Key {
	v, _ := r.Context().Value(ctxKeyAPIKey).(*apikey.Key)
	return v
}

func actorFrom(r *http.Request) string {
	if k := apiKeyFrom(r); k != nil {
		return "key:" + k.Name
	}
	return "anonymous"
}

// ---- /healthz, /version ----------------------------------------------------

func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// Version is set at build time via -ldflags.
var Version = "0.1.0-dev"

func (s *Server) version(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"version": Version})
}

// ---- whoami ----------------------------------------------------------------

func (s *Server) whoami(w http.ResponseWriter, r *http.Request) {
	k := apiKeyFrom(r)
	writeJSON(w, http.StatusOK, k)
}

// ---- secrets ---------------------------------------------------------------

type putSecretBody struct {
	Value    string            `json:"value"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

func (s *Server) listSecrets(w http.ResponseWriter, r *http.Request) {
	out, err := s.Broker.Vault.List(r.Context())
	if err != nil {
		writeServerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"secrets": out})
}

func (s *Server) getSecret(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	pt, err := s.Broker.SecretGet(r.Context(), actorFrom(r), name)
	if err != nil {
		if errors.Is(err, vault.ErrSecretNotFound) {
			writeError(w, http.StatusNotFound, "secret not found")
			return
		}
		writeServerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"value": pt})
}

func (s *Server) headSecret(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	meta, err := s.Broker.Vault.Head(r.Context(), name)
	if err != nil {
		if errors.Is(err, vault.ErrSecretNotFound) {
			writeError(w, http.StatusNotFound, "secret not found")
			return
		}
		writeServerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, meta)
}

func (s *Server) putSecret(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	var body putSecretBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	if body.Value == "" {
		writeError(w, http.StatusBadRequest, "value required")
		return
	}
	out, err := s.Broker.SecretPut(r.Context(), actorFrom(r), name, body.Value, body.Metadata)
	if err != nil {
		writeServerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) deleteSecret(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if err := s.Broker.Vault.Delete(r.Context(), name); err != nil {
		if errors.Is(err, vault.ErrSecretNotFound) {
			writeError(w, http.StatusNotFound, "secret not found")
			return
		}
		writeServerErr(w, err)
		return
	}
	_, _ = s.Audit.Append(r.Context(), actorFrom(r), "secret.delete", name, nil)
	w.WriteHeader(http.StatusNoContent)
}

// ---- hosts -----------------------------------------------------------------

func (s *Server) listHosts(w http.ResponseWriter, r *http.Request) {
	tag := r.URL.Query().Get("tag")
	out, err := s.Broker.Inv.List(r.Context(), tag)
	if err != nil {
		writeServerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"hosts": out})
}

func (s *Server) getHost(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	h, err := s.Broker.Inv.Get(r.Context(), name)
	if err != nil {
		if errors.Is(err, inventory.ErrHostNotFound) {
			writeError(w, http.StatusNotFound, "host not found")
			return
		}
		writeServerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, h)
}

func (s *Server) putHost(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	var h inventory.Host
	if err := json.NewDecoder(r.Body).Decode(&h); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	h.Name = name
	if err := s.Broker.Inv.Upsert(r.Context(), &h); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	_, _ = s.Audit.Append(r.Context(), actorFrom(r), "host.upsert", h.Name, map[string]interface{}{
		"transport": string(h.Transport),
	})
	writeJSON(w, http.StatusOK, h)
}

func (s *Server) deleteHost(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if err := s.Broker.Inv.Delete(r.Context(), name); err != nil {
		if errors.Is(err, inventory.ErrHostNotFound) {
			writeError(w, http.StatusNotFound, "host not found")
			return
		}
		writeServerErr(w, err)
		return
	}
	_, _ = s.Audit.Append(r.Context(), actorFrom(r), "host.delete", name, nil)
	w.WriteHeader(http.StatusNoContent)
}

// ---- exec ------------------------------------------------------------------

type execBody struct {
	Host       string `json:"host,omitempty"`
	Selector   string `json:"selector,omitempty"`
	Command    string `json:"command"`
	TimeoutSec int    `json:"timeout_sec,omitempty"`
	Parallel   int    `json:"parallel,omitempty"`
}

func (s *Server) exec(w http.ResponseWriter, r *http.Request) {
	var body execBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	if body.Host == "" {
		writeError(w, http.StatusBadRequest, "host required")
		return
	}
	if body.Command == "" {
		writeError(w, http.StatusBadRequest, "command required")
		return
	}
	res, err := s.Broker.Exec(r.Context(), actorFrom(r), body.Host, body.Command, broker.ExecOptions{
		Timeout: time.Duration(body.TimeoutSec) * time.Second,
	})
	if err != nil {
		if errors.Is(err, inventory.ErrHostNotFound) {
			writeError(w, http.StatusNotFound, "host not found")
			return
		}
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) execMulti(w http.ResponseWriter, r *http.Request) {
	var body execBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	if body.Selector == "" {
		writeError(w, http.StatusBadRequest, "selector required")
		return
	}
	if body.Command == "" {
		writeError(w, http.StatusBadRequest, "command required")
		return
	}
	out, err := s.Broker.ExecMulti(r.Context(), actorFrom(r), body.Selector, body.Command, body.Parallel, broker.ExecOptions{
		Timeout: time.Duration(body.TimeoutSec) * time.Second,
	})
	if err != nil {
		if errors.Is(err, inventory.ErrHostNotFound) {
			writeError(w, http.StatusNotFound, "no hosts matched selector")
			return
		}
		writeServerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"results": out})
}

// ---- file put / get --------------------------------------------------------

func (s *Server) filePut(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "expected multipart upload: "+err.Error())
		return
	}
	host := r.FormValue("host")
	dst := r.FormValue("dst")
	modeStr := r.FormValue("mode")
	if host == "" || dst == "" {
		writeError(w, http.StatusBadRequest, "host and dst required")
		return
	}
	mode := os.FileMode(0o644)
	if modeStr != "" {
		if v, err := strconv.ParseUint(modeStr, 8, 32); err == nil {
			mode = os.FileMode(v)
		}
	}
	f, _, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "missing file part: "+err.Error())
		return
	}
	defer f.Close()
	if err := s.Broker.Put(r.Context(), actorFrom(r), host, f, dst, mode); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type fileGetBody struct {
	Host string `json:"host"`
	Src  string `json:"src"`
}

func (s *Server) fileGet(w http.ResponseWriter, r *http.Request) {
	var body fileGetBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	if body.Host == "" || body.Src == "" {
		writeError(w, http.StatusBadRequest, "host and src required")
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+sanitizeFilename(body.Src)+`"`)
	w.WriteHeader(http.StatusOK)
	if err := s.Broker.Get(r.Context(), actorFrom(r), body.Host, body.Src, w); err != nil {
		// Stream already started; can't change status, but log via audit on broker side.
		return
	}
}

// ---- audit -----------------------------------------------------------------

func (s *Server) tailAudit(w http.ResponseWriter, r *http.Request) {
	limit := 100
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	out, err := s.Audit.Tail(r.Context(), limit)
	if err != nil {
		writeServerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"records": out})
}

func (s *Server) verifyAudit(w http.ResponseWriter, r *http.Request) {
	bad, err := s.Audit.Verify(r.Context())
	resp := map[string]interface{}{
		"ok": err == nil,
	}
	if err != nil {
		resp["error"] = err.Error()
		resp["bad_record"] = bad
	}
	writeJSON(w, http.StatusOK, resp)
}

// ---- api keys --------------------------------------------------------------

type mintBody struct {
	Name   string   `json:"name"`
	Scopes []string `json:"scopes"`
}

func (s *Server) mintAPIKey(w http.ResponseWriter, r *http.Request) {
	var body mintBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	raw, k, err := s.APIKeys.Mint(r.Context(), body.Name, body.Scopes)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	_, _ = s.Audit.Append(r.Context(), actorFrom(r), "apikey.mint", k.Name, map[string]interface{}{
		"id":     k.ID,
		"scopes": k.Scopes,
	})
	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"key":  raw,
		"meta": k,
	})
}

func (s *Server) listAPIKeys(w http.ResponseWriter, r *http.Request) {
	out, err := s.APIKeys.List(r.Context())
	if err != nil {
		writeServerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"keys": out})
}

func (s *Server) revokeAPIKey(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := s.APIKeys.Revoke(r.Context(), id); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	_, _ = s.Audit.Append(r.Context(), actorFrom(r), "apikey.revoke", id, nil)
	w.WriteHeader(http.StatusNoContent)
}

// ---- helpers ---------------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func writeServerErr(w http.ResponseWriter, err error) {
	writeError(w, http.StatusInternalServerError, err.Error())
}

func sanitizeFilename(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		p = p[i+1:]
	}
	if p == "" {
		return "file"
	}
	return p
}

// keep `os` import alive via FileMode helpers used above.
var _ = os.FileMode(0)
