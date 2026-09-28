package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"controlplane/internal/agentidentity"
	"controlplane/internal/audit"
	"controlplane/internal/billing"
	"controlplane/internal/catalog"
	"controlplane/internal/entitlement"
	"controlplane/internal/forward"
	"controlplane/internal/georules"
	"controlplane/internal/identity"
	"controlplane/internal/orchestration"
	"controlplane/internal/platform/config"
	"controlplane/internal/platform/db"
	"controlplane/internal/platform/httpapi"
	"controlplane/internal/proxyaccess"
	"controlplane/internal/routing"
	"controlplane/internal/subscription"
)

func main() {
	cfg, err := config.LoadFrom(os.LookupEnv)
	if err != nil {
		slog.Error("invalid configuration", "error", err)
		os.Exit(2)
	}
	if len(os.Args) == 2 && os.Args[1] == "--healthcheck" {
		if err := checkLocalHealth(context.Background(), cfg.HTTPAddr); err != nil {
			slog.Error("healthcheck failed", "error", err)
			os.Exit(1)
		}
		return
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))
	pool, err := db.Open(context.Background(), cfg.DatabaseURL)
	if err != nil {
		logger.Error("database unavailable", "error", err)
		os.Exit(1)
	}
	defer pool.Close()
	revisionRepository, err := newRevisionRepository(pool, cfg)
	if err != nil {
		logger.Error("relay secret storage unavailable", "error", err)
		os.Exit(2)
	}
	var sessions httpapi.IdentitySessions
	var catalogStore httpapi.CatalogStore
	var entitlementStore httpapi.EntitlementStore
	var accountStore httpapi.AccountStore
	var lineStore httpapi.LineStore
	var forwardStore httpapi.ForwardStore
	var forwardPolicyStore httpapi.ForwardPolicyStore
	var agentTokenStore httpapi.AgentTokenStore
	var proxyAccessStore httpapi.ProxyAccessStore
	var subscriptionStore httpapi.SubscriptionStore
	var usageStore httpapi.UsageStore
	var routingStore httpapi.RoutingStore
	var geoRuleSetStore httpapi.GeoRuleSetStore
	var roleStore httpapi.RoleStore
	var auditStore httpapi.AuditStore
	if cfg.BrowserAuthEnabled {
		identityRepository := identity.NewPostgresRepository(pool)
		sessions = identity.NewService(identityRepository)
		accountStore = identityRepository
		roleStore = identityRepository
		auditStore = audit.NewPostgresRepository(pool)
		catalogRepository := catalog.NewPostgresRepository(pool)
		catalogStore = catalogRepository
		lineStore = catalogRepository
		forwardRepository := forward.NewPostgresRepository(pool)
		forwardStore = forwardRepository
		forwardPolicyStore = forwardRepository
		entitlementStore = entitlement.NewPostgresRepository(pool)
		usageStore = billing.NewPostgresRepository(pool)
		routingStore = routing.NewPostgresRepository(pool)
		geoRuleSetStore = georules.NewPostgresRepository(pool)
		credentialCipher, err := proxyaccess.NewCredentialCipher(cfg.ProxyCredentialKey)
		if err != nil {
			logger.Error("proxy credential encryption unavailable", "error", err)
			os.Exit(2)
		}
		proxyAccessStore = proxyaccess.NewPostgresRepository(pool, credentialCipher)
		subscriptionStore = subscription.NewPostgresRepository(pool, credentialCipher)
		agentTokenStore = agentidentity.NewEnrollmentService(pool, nil)
	}
	handler := httpapi.NewHandlerWithStoresOptions(logger, db.HealthCheck{Database: pool}, sessions, httpapi.RouteStores{
		Catalog: catalogStore, Entitlements: entitlementStore, Accounts: accountStore, Lines: lineStore,
		Forward: forwardStore, Policies: forwardPolicyStore, AgentTokens: agentTokenStore, ProxyAccess: proxyAccessStore, Subscriptions: subscriptionStore,
		Usage:       usageStore,
		Routing:     routingStore,
		GeoRuleSets: geoRuleSetStore,
		Roles:       roleStore,
		Audit:       auditStore,
	}, httpapi.HandlerOptions{BrowserCookieSecure: cfg.BrowserCookieSecure})
	if cfg.WebDir != "" {
		if _, err := os.Stat(cfg.WebDir + "/index.html"); err != nil {
			logger.Error("web bundle unavailable", "error", err)
			os.Exit(2)
		}
		handler = httpapi.NewWebHandler(handler, os.DirFS(cfg.WebDir))
	}
	server := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if _, err := startConfiguredAgentServer(ctx, cfg, pool, logger, stop); err != nil {
		logger.Error("Agent TLS listener unavailable", "error", err)
		os.Exit(1)
	}
	if cfg.BrowserAuthEnabled {
		go identity.RunSessionJanitor(ctx, identity.NewPostgresRepository(pool), logger)
	}
	go billing.NewPeriodWorker(billing.NewPostgresRepository(pool)).Run(ctx, logger)
	go orchestration.NewConvergenceWorker(pool, revisionRepository).Run(ctx, logger)
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			logger.Error("shutdown failed", "error", err)
		}
	}()
	logger.Info("control plane listening", "address", cfg.HTTPAddr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("server failed", "error", err)
		os.Exit(1)
	}
}
