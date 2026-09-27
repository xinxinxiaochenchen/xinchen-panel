package agentclient

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"time"

	"controlplane/internal/agentproto"
	"controlplane/internal/agentruntime"
	"github.com/coder/websocket"
)

type controlMessage struct {
	envelope agentproto.Envelope
	err      error
}

// serveMessages uses a single reader and a single state machine for snapshots
// and usage ACKs: configuration renewals cannot be mistaken for usage replies.
func (c *Client) serveMessages(ctx context.Context, conn *websocket.Conn, runtime Runtime, send func(agentproto.MessageType, any) error, quota *QuotaExchange) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	incoming := make(chan controlMessage, 1)
	go func() {
		for {
			kind, raw, err := conn.Read(ctx)
			item := controlMessage{err: err}
			if err == nil {
				if kind != websocket.MessageText {
					item.err = errors.New("control plane sent non-text frame")
				} else {
					item.envelope, item.err = agentproto.Decode(raw)
				}
			}
			select {
			case incoming <- item:
			case <-ctx.Done():
				return
			}
			if item.err != nil {
				return
			}
		}
	}()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	applied := int64(0)
	appliedDigest := ""
	var pending *agentproto.ConfigSnapshot
	var inFlight *agentproto.UsageBatch
	var usageSentAt time.Time
	var pendingCertificate []byte
	var pendingFingerprint string
	var confirmedFingerprint string
	var certificateSentAt time.Time
	sendCertificateUpdate := func() error {
		if len(pendingCertificate) == 0 {
			return nil
		}
		if err := send(agentproto.TypeCertificateUpdate, agentproto.CertificateUpdate{CertificatePEM: string(pendingCertificate)}); err != nil {
			return err
		}
		certificateSentAt = time.Now()
		return nil
	}
	recoveryDone := c.config.LeaseStore == nil
	recoveryStarted := false
	recoveryResult := make(chan error, 1)
	startRecovery := func() {
		if recoveryDone || recoveryStarted || c.config.UsageOutbox == nil || len(c.config.UsageOutbox.Pending()) != 0 {
			return
		}
		recoveryStarted = true
		if recoverer, ok := runtime.(interface{ SettleRecovered(context.Context) error }); ok {
			go func() { recoveryResult <- recoverer.SettleRecovered(ctx) }()
		} else {
			recoveryResult <- errors.New("runtime cannot settle recovered leases")
		}
	}
	var leaseUntil time.Time
	timer := time.NewTimer(time.Hour)
	timer.Stop()
	defer timer.Stop()
	watcher := time.AfterFunc(time.Hour, func() {
		cancel()
		_ = runtime.Close()
	})
	watcher.Stop()
	defer watcher.Stop()
	setDeadline := func(until time.Time) {
		leaseUntil = until
		timer.Reset(time.Until(until))
		watcher.Reset(time.Until(until))
	}
	sendUsage := func() error {
		if c.config.UsageOutbox == nil || inFlight != nil {
			return nil
		}
		batches := c.config.UsageOutbox.Pending()
		if len(batches) == 0 {
			return nil
		}
		inFlight = &batches[0]
		usageSentAt = time.Now()
		return send(agentproto.TypeUsageBatch, *inFlight)
	}
	if err := sendUsage(); err != nil {
		return err
	}
	startRecovery()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return errors.New("Agent configuration lease expired")
		case err := <-recoveryResult:
			if err != nil {
				return err
			}
			recoveryDone = true
		case <-ticker.C:
			if len(pendingCertificate) != 0 && time.Since(certificateSentAt) >= 10*time.Second {
				if err := sendCertificateUpdate(); err != nil {
					return err
				}
			}
			if inFlight != nil && time.Since(usageSentAt) >= 10*time.Second {
				return errors.New("Agent usage acknowledgement timed out")
			}
			if err := sendUsage(); err != nil {
				return err
			}
			startRecovery()
		case certificatePEM := <-c.certificateUpdates:
			block, rest := pem.Decode(certificatePEM)
			if block == nil || block.Type != "CERTIFICATE" || len(rest) != 0 {
				return errors.New("invalid pending Agent certificate")
			}
			sum := sha256.Sum256(block.Bytes)
			pendingCertificate = certificatePEM
			pendingFingerprint = hex.EncodeToString(sum[:])
			if err := sendCertificateUpdate(); err != nil {
				return err
			}
		case item := <-incoming:
			if item.err != nil {
				return item.err
			}
			message := item.envelope
			if message.NodeID != c.config.NodeID || !freshMessage(message.SentAt) {
				return errors.New("invalid control plane message")
			}
			switch message.Type {
			case agentproto.TypeCertificateUpdateAck:
				ack, err := agentproto.DecodeCertificateUpdateAck(message.Payload)
				if err != nil {
					break
				}
				if pendingFingerprint != "" && ack.Fingerprint == pendingFingerprint {
					confirmedFingerprint = ack.Fingerprint
					pendingCertificate = nil
					pendingFingerprint = ""
				} else if ack.Fingerprint != confirmedFingerprint {
					break
				}
			case agentproto.TypeQuotaGrant:
				grant, err := agentproto.DecodeQuotaGrant(message.Payload)
				if err != nil {
					return err
				}
				if err := quota.DeliverGrant(grant); err != nil {
					return err
				}
			case agentproto.TypeQuotaDenied:
				denied, err := agentproto.DecodeQuotaDenied(message.Payload)
				if err != nil {
					return err
				}
				if err := quota.DeliverDenied(denied); err != nil {
					return err
				}
			case agentproto.TypeQuotaSettled:
				settled, err := agentproto.DecodeQuotaSettled(message.Payload)
				if err != nil {
					return err
				}
				if err := quota.DeliverSettled(settled); err != nil {
					return err
				}
			case agentproto.TypeUsageAck:
				ack, err := agentproto.DecodeUsageAck(message.Payload)
				if err != nil || inFlight == nil || ack.BatchID != inFlight.BatchID {
					return errors.New("unexpected usage acknowledgement")
				}
				if err := c.config.UsageOutbox.Acknowledge(ack); err != nil {
					return err
				}
				if handler, ok := runtime.(interface{ HandleUsageAck(string) error }); ok {
					go func() {
						if err := handler.HandleUsageAck(ack.BatchID); err != nil {
							cancel()
						}
					}()
				}
				inFlight = nil
				if err := sendUsage(); err != nil {
					return err
				}
				startRecovery()
			case agentproto.TypeConfigSnapshot:
				snapshot, err := agentproto.DecodeConfigSnapshot(message.Payload, time.Now())
				if err != nil {
					return fmt.Errorf("invalid configuration snapshot: %w", err)
				}
				if err := validateReceivedRevision(applied, appliedDigest, snapshot.Revision, snapshot.SHA256); err != nil {
					return err
				}
				if pending != nil {
					if err := validateReceivedRevision(pending.Revision, pending.SHA256, snapshot.Revision, snapshot.SHA256); err != nil {
						return err
					}
				}
				pending = &snapshot
				// Before the first apply, reconciliation is bounded by the offered lease.
				if applied == 0 && leaseUntil.IsZero() {
					setDeadline(snapshot.ValidUntil)
				}
			default:
				return errors.New("unsupported control plane message")
			}
		}
		// Reconcile reports left by a previous runtime before opening listeners.
		// Once running, revocations must still apply immediately even with usage pending.
		if pending == nil || (applied == 0 && (inFlight != nil || !recoveryDone)) {
			continue
		}
		snapshot := *pending
		pending = nil
		if !snapshot.ValidUntil.After(time.Now()) {
			return errors.New("Agent configuration lease expired")
		}
		if leaseUntil.IsZero() || snapshot.ValidUntil.Before(leaseUntil) {
			setDeadline(snapshot.ValidUntil)
		}
		if snapshot.Revision > applied {
			applyUntil := snapshot.ValidUntil
			if !leaseUntil.IsZero() && leaseUntil.Before(applyUntil) {
				applyUntil = leaseUntil
			}
			if soon := time.Now().Add(20 * time.Second); soon.Before(applyUntil) {
				applyUntil = soon
			}
			applyCtx, applyCancel := context.WithDeadline(ctx, applyUntil)
			err := runtime.Apply(applyCtx, agentruntime.Snapshot{Revision: uint64(snapshot.Revision), Rules: snapshot.ForwardConfig, ProxyConfig: snapshot.ProxyConfig, RelayConfig: snapshot.RelayConfig})
			applyCancel()
			if err != nil {
				if sendErr := send(agentproto.TypeConfigResult, agentproto.ConfigResult{Revision: snapshot.Revision, SHA256: snapshot.SHA256, Status: "rejected", ErrorCode: "APPLY_FAILED", ErrorMessage: "configuration could not be applied"}); sendErr != nil {
					return sendErr
				}
				continue
			}
			applied = snapshot.Revision
			appliedDigest = snapshot.SHA256
		}
		if err := c.state.Save(snapshot); err != nil {
			return fmt.Errorf("persist applied Agent snapshot: %w", err)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		setDeadline(snapshot.ValidUntil)
		if err := send(agentproto.TypeConfigResult, agentproto.ConfigResult{Revision: snapshot.Revision, SHA256: snapshot.SHA256, Status: "applied"}); err != nil {
			return err
		}
	}
}
