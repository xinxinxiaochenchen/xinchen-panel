package agentrelay

import (
	"bytes"
	"encoding/binary"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	testLineID       = "11111111-1111-4111-8111-111111111111"
	testConnectionID = "22222222-2222-4222-8222-222222222222"
)

func validOpen(now time.Time) Open {
	return Open{Version: 1, Type: "open", LineID: testLineID, ConnectionID: testConnectionID,
		Generation: 7, TargetHost: "example.com", TargetPort: 443, SentAt: now.UTC().Truncate(time.Second)}
}

func TestOpenFrameRoundTripAndReplayProtection(t *testing.T) {
	now := time.Now().UTC()
	secret := bytes.Repeat([]byte{0x52}, 32)
	request, err := SignOpen(validOpen(now), secret)
	if err != nil {
		t.Fatal(err)
	}
	var wire bytes.Buffer
	if err := WriteOpen(&wire, request); err != nil {
		t.Fatal(err)
	}
	decoded, err := ReadOpen(&wire)
	if err != nil || decoded != request {
		t.Fatalf("round trip: %+v, %v", decoded, err)
	}
	window := NewReplayWindow(8)
	if err := VerifyOpen(decoded, secret, testLineID, 7, window, now); err != nil {
		t.Fatal(err)
	}
	if err := VerifyOpen(decoded, secret, testLineID, 7, window, now); err == nil {
		t.Fatal("replayed connection was accepted")
	}
}

func TestOpenRejectsForgedOrStaleRequests(t *testing.T) {
	now := time.Now().UTC()
	secret := bytes.Repeat([]byte{0x32}, 32)
	request, err := SignOpen(validOpen(now), secret)
	if err != nil {
		t.Fatal(err)
	}
	for name, changed := range map[string]func(*Open){
		"target":     func(v *Open) { v.TargetHost = "evil.example" },
		"generation": func(v *Open) { v.Generation++ },
		"proof":      func(v *Open) { v.Proof = strings.Repeat("0", 64) },
		"timestamp":  func(v *Open) { v.SentAt = v.SentAt.Add(-time.Hour) },
	} {
		t.Run(name, func(t *testing.T) {
			modified := request
			changed(&modified)
			if err := VerifyOpen(modified, secret, testLineID, 7, NewReplayWindow(8), now); err == nil {
				t.Fatal("forged or stale request accepted")
			}
		})
	}
	if err := VerifyOpen(request, secret, testLineID, 8, NewReplayWindow(8), now); err == nil {
		t.Fatal("stale generation accepted")
	}
}

func TestOpenRejectsPrivateTargetsAndOversizedFrames(t *testing.T) {
	now := time.Now().UTC()
	secret := bytes.Repeat([]byte{0x32}, 32)
	for _, host := range []string{"127.0.0.1", "10.0.0.1", "192.168.1.1", "localhost"} {
		request := validOpen(now)
		request.TargetHost = host
		if _, err := SignOpen(request, secret); err == nil {
			t.Fatalf("private target accepted: %s", host)
		}
	}
	var wire bytes.Buffer
	_ = binary.Write(&wire, binary.BigEndian, uint32(MaxOpenFrameBytes+1))
	if _, err := ReadOpen(&wire); err == nil {
		t.Fatal("oversized frame accepted")
	}
	var invalid bytes.Buffer
	payload := []byte(`{"version":1,"type":"open","unknown":true}`)
	_ = binary.Write(&invalid, binary.BigEndian, uint32(len(payload)))
	_, _ = invalid.Write(payload)
	if _, err := ReadOpen(&invalid); err == nil {
		t.Fatal("unknown frame field accepted")
	}
}

func TestOpenResponseRoundTrip(t *testing.T) {
	for _, response := range []OpenResponse{
		{Version: 1, Type: "open_ok", ConnectionID: testConnectionID},
		{Version: 1, Type: "open_err", ConnectionID: testConnectionID, ErrorCode: "TARGET_REJECTED"},
	} {
		var wire bytes.Buffer
		if err := WriteOpenResponse(&wire, response); err != nil {
			t.Fatal(err)
		}
		decoded, err := ReadOpenResponse(&wire)
		if err != nil || decoded != response {
			t.Fatalf("response round trip: %+v, %v", decoded, err)
		}
	}
}

func TestOpenFrameLeavesPayloadAndHandlesShortWrites(t *testing.T) {
	request, err := SignOpen(validOpen(time.Now()), bytes.Repeat([]byte{1}, 32))
	if err != nil {
		t.Fatal(err)
	}
	var wire bytes.Buffer
	if err := WriteOpen(shortWriter{&wire}, request); err != nil {
		t.Fatal(err)
	}
	_, _ = wire.WriteString("client data immediately after handshake")
	if _, err := ReadOpen(&wire); err != nil {
		t.Fatal(err)
	}
	if wire.String() != "client data immediately after handshake" {
		t.Fatal("frame decoder consumed stream payload")
	}
	if err := WriteOpen(zeroWriter{}, request); err != io.ErrShortWrite {
		t.Fatalf("non-progressing writer: %v", err)
	}
}

type shortWriter struct{ io.Writer }

func (writer shortWriter) Write(value []byte) (int, error) {
	return writer.Writer.Write(value[:1])
}

type zeroWriter struct{}

func (zeroWriter) Write([]byte) (int, error) { return 0, nil }

func TestOpenFrameRejectsAmbiguousKeys(t *testing.T) {
	request, _ := SignOpen(validOpen(time.Now()), bytes.Repeat([]byte{1}, 32))
	var original bytes.Buffer
	if err := WriteOpen(&original, request); err != nil {
		t.Fatal(err)
	}
	base := original.Bytes()[4:]
	for name, payload := range map[string][]byte{
		"duplicate":  append([]byte(`{"version":1,`), base[1:]...),
		"case alias": bytes.Replace(base, []byte(`"version"`), []byte(`"Version"`), 1),
		"trailing":   append(append([]byte(nil), base...), []byte(`{}`)...),
	} {
		t.Run(name, func(t *testing.T) {
			var wire bytes.Buffer
			_ = binary.Write(&wire, binary.BigEndian, uint32(len(payload)))
			_, _ = wire.Write(payload)
			if _, err := ReadOpen(&wire); err == nil {
				t.Fatal("ambiguous frame accepted")
			}
		})
	}
}

func TestOpenVerificationDoesNotConsumeCapacityOnInvalidProof(t *testing.T) {
	now := time.Now().UTC()
	secret := bytes.Repeat([]byte{1}, 32)
	request, _ := SignOpen(validOpen(now), secret)
	window := NewReplayWindow(1)
	bad := request
	bad.Proof = strings.Repeat("0", 64)
	if err := VerifyOpen(bad, secret, testLineID, 7, window, now); err == nil {
		t.Fatal("invalid proof accepted")
	}
	if err := VerifyOpen(request, secret, testLineID, 7, window, now); err != nil {
		t.Fatalf("invalid proof consumed capacity: %v", err)
	}
	other := validOpen(now)
	other.ConnectionID = "33333333-3333-4333-8333-333333333333"
	other, _ = SignOpen(other, secret)
	if err := VerifyOpen(other, secret, testLineID, 7, window, now); err == nil {
		t.Fatal("full replay cache evicted a live entry")
	}
	later := now.Add(2 * time.Minute)
	other = validOpen(later)
	other, _ = SignOpen(other, secret)
	if err := VerifyOpen(other, secret, testLineID, 7, window, later); err != nil {
		t.Fatalf("expired replay entry did not release capacity: %v", err)
	}
}

func TestOpenConcurrentReplayAdmitsOnlyOnce(t *testing.T) {
	now := time.Now().UTC()
	secret := bytes.Repeat([]byte{1}, 32)
	request, _ := SignOpen(validOpen(now), secret)
	window := NewReplayWindow(32)
	results := make(chan error, 32)
	var group sync.WaitGroup
	for range 32 {
		group.Go(func() { results <- VerifyOpen(request, secret, testLineID, 7, window, now) })
	}
	group.Wait()
	close(results)
	accepted := 0
	for err := range results {
		if err == nil {
			accepted++
		}
	}
	if accepted != 1 {
		t.Fatalf("accepted %d simultaneous duplicate requests", accepted)
	}
}

func TestOpenExpiryBoundaryCannotReplay(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	secret := bytes.Repeat([]byte{1}, 32)
	request, _ := SignOpen(validOpen(now.Add(handshakeSkew)), secret)
	window := NewReplayWindow(1)
	if err := VerifyOpen(request, secret, testLineID, 7, window, now); err != nil {
		t.Fatal(err)
	}
	if err := VerifyOpen(request, secret, testLineID, 7, window, now.Add(2*handshakeSkew)); err == nil {
		t.Fatal("request replayed at timestamp/cache expiry boundary")
	}
}

func FuzzReadOpen(f *testing.F) {
	request, _ := SignOpen(validOpen(time.Now()), bytes.Repeat([]byte{1}, 32))
	var wire bytes.Buffer
	_ = WriteOpen(&wire, request)
	f.Add(wire.Bytes())
	f.Add([]byte{0, 0, 0x10, 0x01})
	f.Fuzz(func(t *testing.T, data []byte) {
		value, err := ReadOpen(bytes.NewReader(data))
		if err == nil {
			var encoded bytes.Buffer
			if err := WriteOpen(&encoded, value); err != nil {
				t.Fatalf("accepted frame cannot be encoded: %v", err)
			}
		}
	})
}
