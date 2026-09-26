package agentclient

import (
	"context"
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
func (c *Client) serveMessages(ctx context.Context, conn *websocket.Conn, runtime Runtime, send func(agentproto.MessageType, any) error) error {
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
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return errors.New("Agent configuration lease expired")
		case <-ticker.C:
			if inFlight != nil && time.Since(usageSentAt) >= 10*time.Second {
				return errors.New("Agent usage acknowledgement timed out")
			}
			if err := sendUsage(); err != nil {
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
			case agentproto.TypeUsageAck:
				ack, err := agentproto.DecodeUsageAck(message.Payload)
				if err != nil || inFlight == nil || ack.BatchID != inFlight.BatchID {
					return errors.New("unexpected usage acknowledgement")
				}
				if err := c.config.UsageOutbox.Acknowledge(ack); err != nil {
					return err
				}
				inFlight = nil
				if err := sendUsage(); err != nil {
					return err
				}
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
		if pending == nil || (applied == 0 && inFlight != nil) {
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
			err := runtime.Apply(applyCtx, agentruntime.Snapshot{Revision: uint64(snapshot.Revision), Rules: snapshot.ForwardConfig, ProxyConfig: snapshot.ProxyConfig})
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
