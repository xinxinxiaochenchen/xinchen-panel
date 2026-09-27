package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"time"

	"controlplane/internal/agentidentity"
	"controlplane/internal/billing"
	"controlplane/internal/orchestration"
	"controlplane/internal/platform/config"
	"controlplane/internal/platform/httpapi"
	"github.com/jackc/pgx/v5/pgxpool"
)

func startConfiguredAgentServer(ctx context.Context, cfg config.Config, pool *pgxpool.Pool,
	logger *slog.Logger, stop context.CancelFunc) (net.Listener, error) {
	if cfg.AgentTLSAddr == "" {
		return nil, nil
	}
	caCertificate, err := os.ReadFile(cfg.AgentCACertFile)
	if err != nil {
		return nil, fmt.Errorf("read Agent CA certificate: %w", err)
	}
	caKey, err := os.ReadFile(cfg.AgentCAKeyFile)
	if err != nil {
		return nil, fmt.Errorf("read Agent CA key: %w", err)
	}
	issuer, err := agentidentity.NewIssuer(caCertificate, caKey)
	if err != nil {
		return nil, fmt.Errorf("initialize Agent CA: %w", err)
	}
	tlsConfig, err := httpapi.LoadAgentTLS(cfg)
	if err != nil {
		return nil, err
	}
	service := agentidentity.NewEnrollmentService(pool, issuer)
	presence := orchestration.NewAgentPresenceRepository(pool)
	billingRepository := billing.NewPostgresRepository(pool)
	revisionRepository, err := newRevisionRepository(pool, cfg)
	if err != nil {
		return nil, err
	}
	stream := httpapi.NewAgentStreamHandler(ctx, logger, service, revisionRepository, presence,
		orchestration.NewUsageRecorder(billingRepository))
	stream.SetQuotaService(billingRepository)
	listener, err := serveAgentTLS(ctx, cfg.AgentTLSAddr, tlsConfig,
		httpapi.NewAgentHandlerWithStream(logger, service, stream), logger, stop)
	if err != nil {
		return nil, err
	}
	go sweepOfflineAgents(ctx, presence, logger)
	go sweepAgentCertificates(ctx, service, logger)
	return listener, nil
}

func sweepAgentCertificates(ctx context.Context, service *agentidentity.EnrollmentService, logger *slog.Logger) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			queryCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			_, err := service.SweepExpiredGrants(queryCtx)
			cancel()
			if err != nil {
				logger.Warn("Agent certificate grant sweep failed", "error", err)
			}
		}
	}
}

func sweepOfflineAgents(ctx context.Context, presence *orchestration.AgentPresenceRepository, logger *slog.Logger) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			queryCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			_, err := presence.MarkOfflineStale(queryCtx, time.Now().Add(-45*time.Second))
			cancel()
			if err != nil {
				logger.Warn("Agent stale status sweep failed", "error", err)
			}
		}
	}
}

func serveAgentTLS(ctx context.Context, address string, tlsConfig *tls.Config, handler http.Handler,
	logger *slog.Logger, stop context.CancelFunc) (net.Listener, error) {
	listener, err := tls.Listen("tcp", address, tlsConfig)
	if err != nil {
		return nil, fmt.Errorf("listen for Agent TLS: %w", err)
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 15 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: time.Minute}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			logger.Error("Agent TLS shutdown failed", "error", err)
		}
	}()
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("Agent TLS server failed", "error", err)
			stop()
		}
	}()
	return listener, nil
}
