// Package agentrelay defines the bounded handshake used between authenticated
// Agents. TLS peer identity and actual target resolution remain runtime duties.
package agentrelay

import (
	"controlplane/internal/forward"
	"errors"
	"io"
	"regexp"
	"time"
)

const MaxOpenFrameBytes = 4096

const handshakeSkew = 30 * time.Second

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
var proofPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

type Open struct {
	Version      int       `json:"version"`
	Type         string    `json:"type"`
	LineID       string    `json:"line_id"`
	ConnectionID string    `json:"connection_id"`
	Generation   uint64    `json:"generation"`
	TargetHost   string    `json:"target_host"`
	TargetPort   int       `json:"target_port"`
	SentAt       time.Time `json:"sent_at"`
	Proof        string    `json:"proof"`
}

type OpenResponse struct {
	Version      int    `json:"version"`
	Type         string `json:"type"`
	ConnectionID string `json:"connection_id"`
	ErrorCode    string `json:"error_code,omitempty"`
}

func validateOpen(value Open) error {
	if value.Version != 1 || value.Type != "open" && value.Type != "open_udp" || !uuidPattern.MatchString(value.LineID) ||
		!uuidPattern.MatchString(value.ConnectionID) || value.Generation == 0 ||
		len(value.TargetHost) == 0 || len(value.TargetHost) > 253 || !forward.ValidPublicHost(value.TargetHost) ||
		value.TargetPort < 1 || value.TargetPort > 65535 || value.SentAt.IsZero() {
		return errors.New("invalid relay open request")
	}
	return nil
}

func WriteOpen(writer io.Writer, value Open) error {
	if err := validateOpen(value); err != nil {
		return err
	}
	if !proofPattern.MatchString(value.Proof) {
		return errors.New("invalid relay proof")
	}
	return writeFrame(writer, value)
}

func ReadOpen(reader io.Reader) (Open, error) {
	var value Open
	if err := readFrame(reader, &value); err != nil {
		return Open{}, err
	}
	if err := validateOpen(value); err != nil {
		return Open{}, err
	}
	if !proofPattern.MatchString(value.Proof) {
		return Open{}, errors.New("invalid relay proof")
	}
	return value, nil
}

func validateResponse(value OpenResponse) error {
	if value.Version != 1 || !uuidPattern.MatchString(value.ConnectionID) {
		return errors.New("invalid relay open response")
	}
	switch value.Type {
	case "open_ok":
		if value.ErrorCode != "" {
			return errors.New("successful relay response contains an error")
		}
	case "open_err":
		switch value.ErrorCode {
		case "UNAUTHORIZED", "STALE_GENERATION", "TARGET_REJECTED", "UPSTREAM_UNAVAILABLE", "CAPACITY_EXCEEDED":
		default:
			return errors.New("invalid relay error code")
		}
	default:
		return errors.New("invalid relay open response type")
	}
	return nil
}

func WriteOpenResponse(writer io.Writer, value OpenResponse) error {
	if err := validateResponse(value); err != nil {
		return err
	}
	return writeFrame(writer, value)
}

func ReadOpenResponse(reader io.Reader) (OpenResponse, error) {
	var value OpenResponse
	if err := readFrame(reader, &value); err != nil {
		return OpenResponse{}, err
	}
	if err := validateResponse(value); err != nil {
		return OpenResponse{}, err
	}
	return value, nil
}
