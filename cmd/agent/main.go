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
	agentKeyPEM, err := os.ReadFile(cfg.KeyFile)
	if err != nil {
		return fmt.Errorf("read Agent private key: %w", err)
	}
	var relayState *relayRuntimeState
	var relayTLS *tls.Config
	if cfg.RelayHost != "" {
		relayState, err = prepareRelay(ctx, cfg, pair, agentKeyPEM, roots)
		if err != nil {
			return err
		}
		relayTLS, err = relayState.serverTLS()
		if err != nil {
			return fmt.Errorf("configure relay TLS: %w", err)
		}
		go relayState.renewLoop(ctx, cfg.KeyFile)
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
	leases, err := agentclient.NewFileLeaseStore(cfg.StateFile + ".leases.json")
	if err != nil {
		return fmt.Errorf("open Agent lease state: %w", err)
	}
	defer leases.Close()
	var proxyTLS *tls.Config
	if cfg.ProxyCertFile != "" {
		proxyPair, err := tls.LoadX509KeyPair(cfg.ProxyCertFile, cfg.ProxyKeyFile)
		if err != nil {
			return fmt.Errorf("load proxy TLS certificate: %w", err)
		}
		proxyTLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{proxyPair}}
	}
	client, err := agentclient.New(agentclient.Config{URL: cfg.StreamURL, NodeID: cfg.NodeID,
		Version: cfg.Version, RootCAs: roots, Certificate: pair, CertFile: cfg.CertFile, KeyFile: cfg.KeyFile,
		ProxyReady: proxyTLS != nil, RelayReady: relayState != nil, ProxyCandidatesReady: proxyTLS != nil, UsageOutbox: usage, LeaseStore: leases},
		func() agentclient.Runtime {
			options := agentruntime.Options{BindHost: cfg.BindHost, ProxyTLSConfig: proxyTLS, RequireMetering: true}
			if relayState != nil {
				options.RelayPort = cfg.RelayPort
				options.RelayTLSConfig = relayTLS
				options.RelayClientTLS = relayState.clientTLS
			}
			return agentruntime.New(options)
		}, state)
	if err != nil {
		return err
	}
	return client.Run(ctx)
}
