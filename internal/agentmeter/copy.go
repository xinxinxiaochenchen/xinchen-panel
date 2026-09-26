package agentmeter

import "io"

// WriteJournal records a bounded write intent before payload can leave the
// Agent, then checkpoints the count accepted by the destination. A durable
// implementation can conservatively recover an interrupted write intent.
type WriteJournal interface {
	BeforeWrite(Direction, int64) error
	AfterWrite(Direction, int64, int64) error
}

// CopyMetered copies effective payload bytes through one bounded lease. The
// same Budget must be shared by upload and download goroutines. A short write
// counts only bytes accepted by the destination.
func CopyMetered(dst io.Writer, src io.Reader, budget *Budget, direction Direction) (int64, error) {
	return copyMetered(dst, src, budget, direction, nil)
}

func CopyMeteredWithJournal(dst io.Writer, src io.Reader, budget *Budget, direction Direction, journal WriteJournal) (int64, error) {
	if journal == nil {
		return 0, ErrInvalidReservation
	}
	return copyMetered(dst, src, budget, direction, journal)
}

func copyMetered(dst io.Writer, src io.Reader, budget *Budget, direction Direction, journal WriteJournal) (int64, error) {
	var copied int64
	var emptyReads int
	buffer := make([]byte, 16<<10)
	for {
		read, readErr := src.Read(buffer)
		if read < 0 || read > len(buffer) {
			return copied, io.ErrNoProgress
		}
		if read > 0 {
			reservation, err := budget.Reserve(direction, int64(read))
			if err != nil {
				return copied, err
			}
			if journal != nil {
				if err := journal.BeforeWrite(direction, reservation.Bytes); err != nil {
					_ = budget.Commit(reservation, 0)
					return copied, err
				}
			}
			written, writeErr := dst.Write(buffer[:reservation.Bytes])
			if written < 0 || written > int(reservation.Bytes) {
				written = 0
				writeErr = io.ErrShortWrite
			}
			if err := budget.Commit(reservation, int64(written)); err != nil {
				return copied, err
			}
			copied += int64(written)
			if journal != nil {
				if err := journal.AfterWrite(direction, reservation.Bytes, int64(written)); err != nil {
					return copied, err
				}
			}
			if writeErr != nil {
				return copied, writeErr
			}
			if written != int(reservation.Bytes) {
				return copied, io.ErrShortWrite
			}
			if int64(read) > reservation.Bytes {
				return copied, ErrExhausted
			}
		}
		if readErr == io.EOF {
			return copied, nil
		}
		if readErr != nil {
			return copied, readErr
		}
		if read == 0 {
			emptyReads++
			if emptyReads >= 100 {
				return copied, io.ErrNoProgress
			}
		} else {
			emptyReads = 0
		}
	}
}
