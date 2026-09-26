package agentclient

import (
	"context"
	"errors"
	"sync"
	"time"

	"controlplane/internal/agentproto"
)

var ErrQuotaRequestInFlight = errors.New("Agent quota request ID is already in flight")
var ErrUnexpectedQuotaReply = errors.New("unexpected Agent quota reply")
var ErrQuotaExchangeClosed = errors.New("Agent quota exchange is closed")

type QuotaDeniedError struct{ Code string }

func (e *QuotaDeniedError) Error() string { return "Agent quota request denied: " + e.Code }

type quotaReply struct {
	grant   *agentproto.QuotaGrant
	denied  *agentproto.QuotaDenied
	settled *agentproto.QuotaSettled
}

type quotaPending struct {
	connectionID string
	wantsGrant   bool
	result       chan quotaReply
}

// QuotaExchange lets runtime goroutines request leases while the Agent stream
// keeps one reader. Only that reader calls DeliverGrant/Denied/Settled.
type QuotaExchange struct {
	mu      sync.Mutex
	send    func(agentproto.MessageType, any) error
	pending map[string]*quotaPending
	closed  bool
}

func NewQuotaExchange(send func(agentproto.MessageType, any) error) *QuotaExchange {
	return &QuotaExchange{send: send, pending: make(map[string]*quotaPending)}
}

func (q *QuotaExchange) Open(ctx context.Context, value agentproto.ConnectionOpen) (agentproto.QuotaGrant, error) {
	reply, err := q.request(ctx, value.RequestID, value.ConnectionID, true, agentproto.TypeConnectionOpen, value)
	if err != nil {
		return agentproto.QuotaGrant{}, err
	}
	return usableGrant(reply.grant)
}

func (q *QuotaExchange) Renew(ctx context.Context, value agentproto.QuotaRequest) (agentproto.QuotaGrant, error) {
	reply, err := q.request(ctx, value.RequestID, value.ConnectionID, true, agentproto.TypeQuotaRequest, value)
	if err != nil {
		return agentproto.QuotaGrant{}, err
	}
	return usableGrant(reply.grant)
}

func (q *QuotaExchange) Settle(ctx context.Context, value agentproto.QuotaSettle) (agentproto.QuotaSettled, error) {
	reply, err := q.request(ctx, value.RequestID, value.ConnectionID, false, agentproto.TypeQuotaSettle, value)
	if err != nil {
		return agentproto.QuotaSettled{}, err
	}
	if reply.settled == nil || reply.settled.LeaseID != value.LeaseID || reply.settled.ConsumedBytes != value.ConsumedBytes {
		return agentproto.QuotaSettled{}, ErrUnexpectedQuotaReply
	}
	return *reply.settled, nil
}

func usableGrant(value *agentproto.QuotaGrant) (agentproto.QuotaGrant, error) {
	if value == nil || value.LeaseState != "active" || !value.ExpiresAt.After(time.Now()) {
		return agentproto.QuotaGrant{}, ErrUnexpectedQuotaReply
	}
	return *value, nil
}

func (q *QuotaExchange) request(ctx context.Context, requestID, connectionID string, wantsGrant bool,
	kind agentproto.MessageType, payload any) (quotaReply, error) {
	pending := &quotaPending{connectionID: connectionID, wantsGrant: wantsGrant, result: make(chan quotaReply, 1)}
	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		return quotaReply{}, ErrQuotaExchangeClosed
	}
	if _, exists := q.pending[requestID]; exists {
		q.mu.Unlock()
		return quotaReply{}, ErrQuotaRequestInFlight
	}
	if len(q.pending) >= 4096 {
		q.mu.Unlock()
		return quotaReply{}, ErrQuotaRequestInFlight
	}
	q.pending[requestID] = pending
	q.mu.Unlock()
	defer q.unregister(requestID, pending)
	if err := q.send(kind, payload); err != nil {
		return quotaReply{}, err
	}
	select {
	case <-ctx.Done():
		return quotaReply{}, ctx.Err()
	case reply, ok := <-pending.result:
		if !ok {
			return quotaReply{}, ErrQuotaExchangeClosed
		}
		q.mu.Lock()
		closed := q.closed
		q.mu.Unlock()
		if closed {
			return quotaReply{}, ErrQuotaExchangeClosed
		}
		if reply.denied != nil {
			return quotaReply{}, &QuotaDeniedError{Code: reply.denied.ErrorCode}
		}
		return reply, nil
	}
}

func (q *QuotaExchange) unregister(requestID string, pending *quotaPending) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.pending[requestID] == pending {
		delete(q.pending, requestID)
	}
}

func (q *QuotaExchange) deliver(requestID, connectionID string, reply quotaReply, isGrant bool) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	pending := q.pending[requestID]
	if pending == nil || pending.connectionID != connectionID ||
		(reply.denied == nil && pending.wantsGrant != isGrant) {
		return ErrUnexpectedQuotaReply
	}
	delete(q.pending, requestID)
	pending.result <- reply
	return nil
}

func (q *QuotaExchange) DeliverGrant(value agentproto.QuotaGrant) error {
	return q.deliver(value.RequestID, value.ConnectionID, quotaReply{grant: &value}, true)
}

func (q *QuotaExchange) DeliverDenied(value agentproto.QuotaDenied) error {
	return q.deliver(value.RequestID, value.ConnectionID, quotaReply{denied: &value}, false)
}

func (q *QuotaExchange) DeliverSettled(value agentproto.QuotaSettled) error {
	return q.deliver(value.RequestID, value.ConnectionID, quotaReply{settled: &value}, false)
}

func (q *QuotaExchange) Close() {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return
	}
	q.closed = true
	for requestID, pending := range q.pending {
		delete(q.pending, requestID)
		close(pending.result)
	}
}
