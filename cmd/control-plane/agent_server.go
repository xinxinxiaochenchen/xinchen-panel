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
	return serveAgentTLS(ctx, cfg.AgentTLSAddr, tlsConfig, httpapi.NewAgentHandler(logger, service), logger, stop)
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
