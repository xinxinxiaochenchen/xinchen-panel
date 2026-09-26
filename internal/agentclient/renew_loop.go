package agentclient

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"time"

	"controlplane/internal/agentidentity"
)

func (c *Client) renewLoop(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		if err := c.renewIfNeeded(ctx); err != nil && ctx.Err() == nil {
			slog.Warn("Agent certificate renewal failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (c *Client) renewIfNeeded(ctx context.Context) error {
	pair, err := tls.LoadX509KeyPair(c.config.CertFile, c.config.KeyFile)
	if err != nil {
		return fmt.Errorf("load Agent renewal certificate: %w", err)
	}
	certificate := pair.Leaf
	if certificate == nil {
		certificate, err = x509.ParseCertificate(pair.Certificate[0])
		if err != nil {
			return err
		}
	}
	remaining := time.Until(certificate.NotAfter)
	if remaining > agentidentity.CertificateRenewalWindow {
		return nil
	}
	if remaining <= 0 {
		return fmt.Errorf("Agent certificate has expired")
	}
	keyPEM, err := os.ReadFile(c.config.KeyFile)
	if err != nil {
		return err
	}
	endpoint, err := url.Parse(c.config.URL)
	if err != nil {
		return err
	}
	endpoint.Scheme = "https"
	endpoint.Path = "/api/v1/agent/renew"
	requestCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	renewed, err := RenewCertificate(requestCtx, endpoint.String(), c.config.RootCAs, pair, keyPEM,
		c.config.Version, c.config.NodeID)
	if err != nil {
		return err
	}
	if err := SaveRenewedCertificate(c.config.CertFile, c.config.KeyFile, renewed.CertPEM); err != nil {
		return err
	}
	c.httpClient.Transport.(*http.Transport).CloseIdleConnections()
	select {
	case c.certificateUpdates <- renewed.CertPEM:
	default:
		select {
		case <-c.certificateUpdates:
		default:
		}
		c.certificateUpdates <- renewed.CertPEM
	}
	return nil
}
