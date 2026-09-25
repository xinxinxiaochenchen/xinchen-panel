package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"
)

var fallbackRequestID atomic.Uint64

type ReadyChecker interface {
	Check(context.Context) error
}

func NewHandler(logger *slog.Logger, checker ReadyChecker) http.Handler {
	return NewHandlerWithIdentity(logger, checker, nil)
}

func NewHandlerWithIdentity(logger *slog.Logger, checker ReadyChecker, sessions IdentitySessions) http.Handler {
	return NewHandlerWithCatalog(logger, checker, sessions, nil)
}

func NewHandlerWithCatalog(logger *slog.Logger, checker ReadyChecker, sessions IdentitySessions, catalog CatalogStore) http.Handler {
	return NewHandlerWithEntitlements(logger, checker, sessions, catalog, nil)
}

func NewHandlerWithEntitlements(logger *slog.Logger, checker ReadyChecker, sessions IdentitySessions, catalog CatalogStore, entitlements EntitlementStore) http.Handler {
	return NewHandlerWithAccounts(logger, checker, sessions, catalog, entitlements, nil)
}

func NewHandlerWithAccounts(logger *slog.Logger, checker ReadyChecker, sessions IdentitySessions, catalog CatalogStore, entitlements EntitlementStore, accounts AccountStore) http.Handler {
	return NewHandlerWithLines(logger, checker, sessions, catalog, entitlements, accounts, nil)
}

func NewHandlerWithLines(logger *slog.Logger, checker ReadyChecker, sessions IdentitySessions, catalog CatalogStore, entitlements EntitlementStore, accounts AccountStore, lines LineStore) http.Handler {
	mux := http.NewServeMux()
	live := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			WriteError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}
	mux.HandleFunc("/api/v1/health/live", live)
	mux.HandleFunc("/api/v1/health/ready", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			WriteError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
			return
		}
		if checker != nil {
			ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
			defer cancel()
			if err := checker.Check(ctx); err != nil {
				logger.Warn("readiness dependency unavailable", "error", err)
				WriteError(w, r, http.StatusServiceUnavailable, "DEPENDENCY_UNAVAILABLE", "dependency unavailable")
				return
			}
		}
		live(w, r)
	})
	if sessions != nil {
		registerIdentityRoutes(mux, sessions)
		if catalog != nil {
			registerCatalogRoutes(mux, sessions, catalog)
		}
		if entitlements != nil {
			registerEntitlementRoutes(mux, sessions, entitlements)
		}
		if accounts != nil {
			registerAccountRoutes(mux, sessions, accounts)
		}
		if lines != nil {
			registerLineRoutes(mux, sessions, lines)
		}
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		WriteError(w, r, http.StatusNotFound, "NOT_FOUND", "resource not found")
	})
	return withMiddleware(logger, mux)
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(body)
}

func newRequestID() string {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err == nil {
		return hex.EncodeToString(bytes[:])
	}
	return "fallback-" + hex.EncodeToString([]byte(time.Now().UTC().Format(time.RFC3339Nano))) + "-" + hex.EncodeToString([]byte{byte(fallbackRequestID.Add(1))})
}

func withMiddleware(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		requestID := newRequestID()
		w.Header().Set("X-Request-ID", requestID)
		r = r.WithContext(context.WithValue(r.Context(), requestIDKey{}, requestID))
		writer := &statusWriter{ResponseWriter: w}
		defer func() {
			if recovered := recover(); recovered != nil {
				logger.Error("request panic", "request_id", requestID)
				if writer.status == 0 {
					WriteError(writer, r, http.StatusInternalServerError, "INTERNAL", "internal server error")
				}
			}
			logger.Info("http request", "request_id", requestID, "method", r.Method, "path", r.URL.Path, "status", writer.status, "duration_ms", time.Since(started).Milliseconds())
		}()
		next.ServeHTTP(writer, r)
	})
}
