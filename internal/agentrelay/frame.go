package agentrelay

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
)

func writeFrame(writer io.Writer, value any) error {
	payload, err := json.Marshal(value)
	if err != nil || len(payload) > MaxOpenFrameBytes {
		return errors.New("relay frame cannot be encoded within limit")
	}
	frame := make([]byte, 4+len(payload))
	binary.BigEndian.PutUint32(frame[:4], uint32(len(payload)))
	copy(frame[4:], payload)
	for len(frame) > 0 {
		written, err := writer.Write(frame)
		if err != nil {
			return err
		}
		if written <= 0 {
			return io.ErrShortWrite
		}
		frame = frame[written:]
	}
	return nil
}

func readFrame(reader io.Reader, target any) error {
	var header [4]byte
	if _, err := io.ReadFull(reader, header[:]); err != nil {
		return err
	}
	length := binary.BigEndian.Uint32(header[:])
	if length == 0 || length > MaxOpenFrameBytes {
		return errors.New("relay frame length is invalid")
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(reader, payload); err != nil {
		return err
	}
	if err := rejectDuplicateFields(payload, target); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return errors.New("invalid relay frame fields")
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return errors.New("relay frame has trailing data")
	}
	return nil
}

func rejectDuplicateFields(payload []byte, target any) error {
	openFields := map[string]bool{"version": true, "type": true, "line_id": true, "connection_id": true,
		"generation": true, "target_host": true, "target_port": true, "sent_at": true, "proof": true}
	responseFields := map[string]bool{"version": true, "type": true, "connection_id": true, "error_code": true}
	allowed := openFields
	if _, response := target.(*OpenResponse); response {
		allowed = responseFields
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	first, err := decoder.Token()
	if err != nil || first != json.Delim('{') {
		return errors.New("relay frame must be a JSON object")
	}
	seen := make(map[string]bool)
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return errors.New("invalid relay frame field")
		}
		key, ok := token.(string)
		if !ok || seen[key] || !allowed[key] {
			return errors.New("duplicate or invalid relay frame field")
		}
		seen[key] = true
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return errors.New("invalid relay frame value")
		}
	}
	if _, err := decoder.Token(); err != nil {
		return errors.New("invalid relay frame ending")
	}
	return nil
}
