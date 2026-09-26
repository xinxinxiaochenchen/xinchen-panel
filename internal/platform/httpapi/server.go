package httpapi

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"path"
	"strings"
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
	return NewHandlerWithForward(logger, checker, sessions, catalog, entitlements, accounts, lines, nil)
}

func NewHandlerWithForward(logger *slog.Logger, checker ReadyChecker, sessions IdentitySessions, catalog CatalogStore, entitlements EntitlementStore, accounts AccountStore, lines LineStore, forward ForwardStore) http.Handler {
	return NewHandlerWithForwardPolicies(logger, checker, sessions, catalog, entitlements, accounts, lines, forward, nil)
}

func NewHandlerWithForwardPolicies(logger *slog.Logger, checker ReadyChecker, sessions IdentitySessions, catalog CatalogStore, entitlements EntitlementStore, accounts AccountStore, lines LineStore, forward ForwardStore, policies ForwardPolicyStore) http.Handler {
	return NewHandlerWithAgentTokens(logger, checker, sessions, catalog, entitlements, accounts, lines, forward, policies, nil)
}

func NewHandlerWithAgentTokens(logger *slog.Logger, checker ReadyChecker, sessions IdentitySessions, catalog CatalogStore, entitlements EntitlementStore, accounts AccountStore, lines LineStore, forward ForwardStore, policies ForwardPolicyStore, agentTokens AgentTokenStore) http.Handler {
	return NewHandlerWithStores(logger, checker, sessions, RouteStores{Catalog: catalog, Entitlements: entitlements,
		Accounts: accounts, Lines: lines, Forward: forward, Policies: policies, AgentTokens: agentTokens})
}

type RouteStores struct {
	Catalog       CatalogStore
	Entitlements  EntitlementStore
	Accounts      AccountStore
	Lines         LineStore
	Forward       ForwardStore
	Policies      ForwardPolicyStore
	AgentTokens   AgentTokenStore
	ProxyAccess   ProxyAccessStore
	Subscriptions SubscriptionStore
	Usage         UsageStore
	Routing       RoutingStore
	GeoRuleSets   GeoRuleSetStore
	Roles         RoleStore
}

func NewHandlerWithProxyAccess(logger *slog.Logger, checker ReadyChecker, sessions IdentitySessions, proxy ProxyAccessStore) http.Handler {
	return NewHandlerWithStores(logger, checker, sessions, RouteStores{ProxyAccess: proxy})
}

func NewHandlerWithStores(logger *slog.Logger, checker ReadyChecker, sessions IdentitySessions, stores RouteStores) http.Handler {
	catalog, entitlements, accounts, lines := stores.Catalog, stores.Entitlements, stores.Accounts, stores.Lines
	forward, policies, agentTokens := stores.Forward, stores.Policies, stores.AgentTokens
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
		if forward != nil {
			registerForwardRoutes(mux, sessions, forward)
		}
		if policies != nil {
			registerForwardPolicyRoutes(mux, sessions, policies)
		}
		if agentTokens != nil {
			registerAgentTokenRoutes(mux, sessions, agentTokens)
		}
		if stores.ProxyAccess != nil {
			registerProxyAccessRoutes(mux, sessions, stores.ProxyAccess)
		}
		if stores.Subscriptions != nil {
			registerSubscriptionRoutes(mux, sessions, stores.Subscriptions)
		}
		if stores.Usage != nil {
			registerUsageRoutes(mux, sessions, stores.Usage)
		}
		if stores.Routing != nil {
			registerRoutingRoutes(mux, sessions, stores.Routing)
		}
		if stores.GeoRuleSets != nil {
			registerGeoRuleSetRoutes(mux, sessions, stores.GeoRuleSets)
		}
		if stores.Roles != nil {
			registerRoleRoutes(mux, sessions, stores.Roles)
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

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *statusWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, http.ErrNotSupported
	}
	connection, writer, err := hijacker.Hijack()
	if err == nil {
		w.status = http.StatusSwitchingProtocols
	}
	return connection, writer, err
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
		tokenPath := redactedLogPath(r.URL.Path) == "/sub/<redacted>"
		if tokenPath {
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Referrer-Policy", "no-referrer")
		}
		defer func() {
			if recovered := recover(); recovered != nil {
				logger.Error("request panic", "request_id", requestID)
				if writer.status == 0 {
					WriteError(writer, r, http.StatusInternalServerError, "INTERNAL", "internal server error")
				}
			}
			logger.Info("http request", "request_id", requestID, "method", r.Method, "path", redactedLogPath(r.URL.Path), "status", writer.status, "duration_ms", time.Since(started).Milliseconds())
		}()
		if tokenPath && (path.Clean(r.URL.Path) != r.URL.Path || !strings.HasPrefix(r.URL.Path, "/sub/")) {
			WriteError(writer, r, http.StatusNotFound, "NOT_FOUND", "resource not found")
			return
		}
		next.ServeHTTP(writer, r)
	})
}

func redactedLogPath(path string) string {
	if strings.Contains(path, "/sub/") || strings.HasPrefix(path, "sub/") {
		return "/sub/<redacted>"
	}
	return path
}
