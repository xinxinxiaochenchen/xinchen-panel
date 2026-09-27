package agentrelay

import (
	"encoding/binary"
	"errors"
	"io"
)

// MaxDatagramPayloadBytes is the largest UDP payload that can be carried over
// a relay stream. It matches the maximum IPv4 UDP payload and keeps the relay
// from allocating memory for an unbounded peer-controlled length.
const MaxDatagramPayloadBytes = 65507

// WriteDatagram writes one length-prefixed UDP payload. A zero-length payload
// is valid because UDP permits an empty datagram.
func WriteDatagram(writer io.Writer, payload []byte) error {
	if len(payload) > MaxDatagramPayloadBytes {
		return errors.New("datagram payload is too large")
	}
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(payload)))
	if err := writeAll(writer, header[:]); err != nil {
		return err
	}
	return writeAll(writer, payload)
}

// ReadDatagram reads exactly one length-prefixed UDP payload and leaves the
// next frame in the stream untouched.
func ReadDatagram(reader io.Reader) ([]byte, error) {
	var header [4]byte
	if _, err := io.ReadFull(reader, header[:]); err != nil {
		return nil, err
	}
	length := binary.BigEndian.Uint32(header[:])
	if length > MaxDatagramPayloadBytes {
		return nil, errors.New("datagram payload is too large")
	}
	payload := make([]byte, int(length))
	if _, err := io.ReadFull(reader, payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func writeAll(writer io.Writer, payload []byte) error {
	for len(payload) > 0 {
		written, err := writer.Write(payload)
		if err != nil {
			return err
		}
		if written <= 0 {
			return io.ErrShortWrite
		}
		payload = payload[written:]
	}
	return nil
}
