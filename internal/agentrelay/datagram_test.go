package agentrelay

import (
	"bytes"
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"
)

func TestDatagramFrameRoundTripPreservesBoundaries(t *testing.T) {
	var wire bytes.Buffer
	first := []byte("first packet")
	second := bytes.Repeat([]byte{0x7a}, 4096)
	if err := WriteDatagram(&wire, first); err != nil {
		t.Fatal(err)
	}
	if err := WriteDatagram(&wire, second); err != nil {
		t.Fatal(err)
	}
	if err := WriteDatagram(&wire, nil); err != nil {
		t.Fatal(err)
	}
	gotFirst, err := ReadDatagram(&wire)
	if err != nil || !bytes.Equal(gotFirst, first) {
		t.Fatalf("first datagram = %d bytes, %v", len(gotFirst), err)
	}
	gotSecond, err := ReadDatagram(&wire)
	if err != nil || !bytes.Equal(gotSecond, second) {
		t.Fatalf("second datagram = %d bytes, %v", len(gotSecond), err)
	}
	gotEmpty, err := ReadDatagram(&wire)
	if err != nil || len(gotEmpty) != 0 {
		t.Fatalf("empty datagram = %d bytes, %v", len(gotEmpty), err)
	}
	if wire.Len() != 0 {
		t.Fatalf("frame reader consumed %d trailing bytes", wire.Len())
	}
}

func TestDatagramRelayPreservesPartialFrameAcrossOtherDirectionActivity(t *testing.T) {
	client, peer := net.Pipe()
	upstream, target := net.Pipe()
	defer peer.Close()
	defer target.Close()
	finished := make(chan error, 1)
	go func() { finished <- copyDatagramsWithIdle(client, upstream, 100*time.Millisecond) }()
	if _, err := peer.Write([]byte{0, 0}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(60 * time.Millisecond)
	go func() { _, _ = target.Write([]byte("keepalive")) }()
	_ = peer.SetReadDeadline(time.Now().Add(time.Second))
	if response, err := ReadDatagram(peer); err != nil || !bytes.Equal(response, []byte("keepalive")) {
		t.Fatalf("keepalive response=%q, err=%v", response, err)
	}
	time.Sleep(60 * time.Millisecond)
	if _, err := peer.Write([]byte{0, 4, 't', 'e', 's', 't'}); err != nil {
		t.Fatal(err)
	}
	_ = target.SetReadDeadline(time.Now().Add(time.Second))
	packet := make([]byte, 4)
	if _, err := io.ReadFull(target, packet); err != nil || string(packet) != "test" {
		t.Fatalf("partial frame packet=%q, err=%v", packet, err)
	}
	peer.Close()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("UDP relay did not close after peer disconnected")
	}
}

func TestMiddleDatagramRelayRejectsOversizedFrame(t *testing.T) {
	client, sender := net.Pipe()
	upstream, receiver := net.Pipe()
	defer sender.Close()
	defer receiver.Close()
	finished := make(chan error, 1)
	go func() { finished <- copyFramedDatagramsWithIdle(client, upstream, time.Second) }()
	var oversized [4]byte
	binary.BigEndian.PutUint32(oversized[:], MaxDatagramPayloadBytes+1)
	if _, err := sender.Write(oversized[:]); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-finished:
		if err == nil {
			t.Fatal("oversized frame was forwarded")
		}
	case <-time.After(time.Second):
		t.Fatal("oversized middle-hop frame held the connection open")
	}
	_ = receiver.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	if _, err := receiver.Read(make([]byte, 1)); err == nil {
		t.Fatal("upstream remained open after malformed frame")
	}
}

func TestMiddleDatagramRelayClosesWhenPeerStopsReading(t *testing.T) {
	client, sender := net.Pipe()
	upstream, receiver := net.Pipe()
	defer sender.Close()
	defer receiver.Close()
	finished := make(chan error, 1)
	go func() { finished <- copyFramedDatagramsWithIdle(client, upstream, 40*time.Millisecond) }()
	if err := WriteDatagram(sender, []byte("packet")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("unread UDP peer held middle relay writer open")
	}
}

func TestDatagramFrameRejectsInvalidLengthsAndTruncation(t *testing.T) {
	for _, length := range []uint32{MaxDatagramPayloadBytes + 1, ^uint32(0)} {
		var wire bytes.Buffer
		_ = binary.Write(&wire, binary.BigEndian, length)
		if _, err := ReadDatagram(&wire); err == nil {
			t.Fatalf("accepted datagram length %d", length)
		}
	}
	var wire bytes.Buffer
	_ = binary.Write(&wire, binary.BigEndian, uint32(4))
	_, _ = wire.Write([]byte("short"))
	if _, err := ReadDatagram(bytes.NewReader(wire.Bytes()[:7])); err == nil {
		t.Fatal("accepted truncated datagram")
	}
	if err := WriteDatagram(&wire, bytes.Repeat([]byte{1}, MaxDatagramPayloadBytes+1)); err == nil {
		t.Fatal("accepted oversized datagram")
	}
}

func TestDatagramFrameHandlesShortAndNonProgressingWriters(t *testing.T) {
	var wire bytes.Buffer
	payload := []byte("short writes")
	if err := WriteDatagram(shortDatagramWriter{Writer: &wire}, payload); err != nil {
		t.Fatal(err)
	}
	got, err := ReadDatagram(&wire)
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("short writer payload = %q, %v", got, err)
	}
	if err := WriteDatagram(zeroDatagramWriter{}, payload); err != io.ErrShortWrite {
		t.Fatalf("non-progressing writer error = %v", err)
	}
}

type shortDatagramWriter struct{ io.Writer }

func (w shortDatagramWriter) Write(value []byte) (int, error) {
	return w.Writer.Write(value[:1])
}

type zeroDatagramWriter struct{}

func (zeroDatagramWriter) Write([]byte) (int, error) { return 0, nil }
