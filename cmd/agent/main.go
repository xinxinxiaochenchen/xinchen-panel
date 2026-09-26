package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"controlplane/internal/agentclient"
	"controlplane/internal/agentidentity"
	"controlplane/internal/agentruntime"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if len(os.Args) == 2 && os.Args[1] == "enroll" {
		cfg, err := LoadEnrollmentConfig(os.LookupEnv)
		if err != nil {
			logger.Error("invalid enrollment configuration", "error", err)
			os.Exit(2)
		}
		token, err := io.ReadAll(io.LimitReader(os.Stdin, 256))
		if err != nil {
			logger.Error("read enrollment token failed", "error", err)
			os.Exit(2)
		}
		if err := enrollAgent(context.Background(), cfg, string(token)); err != nil {
			logger.Error("Agent enrollment failed", "error", err)
			os.Exit(1)
		}
		logger.Info("Agent credentials enrolled", "node_id", cfg.NodeID)
		return
	}
	cfg, err := LoadAgentConfig(os.LookupEnv)
	if err != nil {
		logger.Error("invalid Agent configuration", "error", err)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := runAgent(ctx, cfg); err != nil && !errors.Is(err, context.Canceled) {
		logger.Error("Agent stopped", "error", err)
		os.Exit(1)
	}
}

func runAgent(ctx context.Context, cfg AgentConfig) error {
	pair, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
	if err != nil {
		return fmt.Errorf("load Agent certificate: %w", err)
	}
	certificate := pair.Leaf
	if certificate == nil {
		certificate, err = x509.ParseCertificate(pair.Certificate[0])
		if err != nil {
			return fmt.Errorf("parse Agent certificate: %w", err)
		}
	}
	nodeID, err := agentidentity.CertificateNodeID(certificate)
	if err != nil || nodeID != cfg.NodeID {
		return errors.New("Agent certificate does not match configured node")
	}
	caPEM, err := os.ReadFile(cfg.CACertFile)
	if err != nil {
		return fmt.Errorf("read control plane CA: %w", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		return errors.New("control plane CA contains no certificate")
	}
	state, err := agentclient.NewFileStateStore(cfg.StateFile)
	if err != nil {
		return err
	}
	usage, err := agentclient.NewFileUsageOutbox(cfg.StateFile + ".usage.json")
	if err != nil {
		return fmt.Errorf("open Agent usage outbox: %w", err)
	}
	defer usage.Close()
	var proxyTLS *tls.Config
	if cfg.ProxyCertFile != "" {
		proxyPair, err := tls.LoadX509KeyPair(cfg.ProxyCertFile, cfg.ProxyKeyFile)
		if err != nil {
			return fmt.Errorf("load proxy TLS certificate: %w", err)
		}
		proxyTLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{proxyPair}}
	}
	client, err := agentclient.New(agentclient.Config{URL: cfg.StreamURL, NodeID: cfg.NodeID,
		Version: cfg.Version, RootCAs: roots, Certificate: pair, ProxyReady: proxyTLS != nil, UsageOutbox: usage},
		func() agentclient.Runtime {
			return agentruntime.New(agentruntime.Options{BindHost: cfg.BindHost, ProxyTLSConfig: proxyTLS, RequireMetering: true})
		}, state)
	if err != nil {
		return err
	}
	return client.Run(ctx)
}
