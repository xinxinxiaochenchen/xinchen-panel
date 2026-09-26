package agentmeter

import (
	"io"
)

// WritePacket never turns a short quota allowance into a partial datagram.
func WritePacket(dst io.Writer, payload []byte, budget *Budget, direction Direction) (int, error) {
	return writePacket(dst, payload, budget, direction, nil)
}

func WritePacketWithJournal(dst io.Writer, payload []byte, budget *Budget, direction Direction, journal WriteJournal) (int, error) {
	if journal == nil {
		return 0, ErrInvalidReservation
	}
	return writePacket(dst, payload, budget, direction, journal)
}

func writePacket(dst io.Writer, payload []byte, budget *Budget, direction Direction, journal WriteJournal) (int, error) {
	if len(payload) == 0 {
		return 0, ErrInvalidReservation
	}
	reservation, err := budget.Reserve(direction, int64(len(payload)))
	if err != nil {
		return 0, err
	}
	if reservation.Bytes != int64(len(payload)) {
		_ = budget.Commit(reservation, 0)
		return 0, ErrExhausted
	}
	if journal != nil {
		if err := journal.BeforeWrite(direction, reservation.Bytes); err != nil {
			_ = budget.Commit(reservation, 0)
			return 0, err
		}
	}
	n, writeErr := dst.Write(payload)
	if n < 0 || n > len(payload) {
		n = 0
		writeErr = io.ErrShortWrite
	}
	if err := budget.Commit(reservation, int64(n)); err != nil {
		return n, err
	}
	if journal != nil {
		if err := journal.AfterWrite(direction, reservation.Bytes, int64(n)); err != nil {
			return n, err
		}
	}
	if writeErr == nil && n != len(payload) {
		writeErr = io.ErrShortWrite
	}
	return n, writeErr
}
