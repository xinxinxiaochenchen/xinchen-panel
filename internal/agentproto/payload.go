package agentproto

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"time"
	"unicode"
	"unicode/utf8"

	"controlplane/internal/agentruntime"
)

type ConfigSnapshot struct {
	Revision      int64                      `json:"revision"`
	SHA256        string                     `json:"sha256"`
	ValidUntil    time.Time                  `json:"valid_until"`
	ForwardConfig []agentruntime.Rule        `json:"forward_config"`
	ProxyConfig   []agentruntime.ProxyAccess `json:"proxy_config,omitempty"`
	RelayConfig   []agentruntime.RelayConfig `json:"relay_config,omitempty"`
}

type ConfigResult struct {
	Revision     int64  `json:"revision"`
	SHA256       string `json:"sha256"`
	Status       string `json:"status"`
	ErrorCode    string `json:"error_code,omitempty"`
	ErrorMessage string `json:"error_message,omitempty"`
}

var digestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
var errorCodePattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`)

// CanonicalForwardConfig is the one executable encoding used by the control
// plane digest and by the Agent before applying a received snapshot.
func CanonicalForwardConfig(input []agentruntime.Rule) ([]byte, string, error) {
	return CanonicalConfig(input, nil)
}

// CanonicalConfig includes all executable configuration in one digest. Empty
// proxy configuration is omitted so existing forward-only revisions retain
// their wire encoding and checksum during an upgrade.
func CanonicalConfig(input []agentruntime.Rule, proxyInput []agentruntime.ProxyAccess) ([]byte, string, error) {
	return CanonicalConfigWithRelay(input, proxyInput, nil)
}

func CanonicalConfigWithRelay(input []agentruntime.Rule, proxyInput []agentruntime.ProxyAccess, relayInput []agentruntime.RelayConfig) ([]byte, string, error) {
	rules := append([]agentruntime.Rule(nil), input...)
	proxies := append([]agentruntime.ProxyAccess(nil), proxyInput...)
	relays := append([]agentruntime.RelayConfig(nil), relayInput...)
	for _, rule := range rules {
		if !rule.Enabled {
			return nil, "", errors.New("executable forward config contains a disabled rule")
		}
	}
	sort.Slice(rules, func(left, right int) bool { return rules[left].ID < rules[right].ID })
	sort.Slice(proxies, func(left, right int) bool { return proxies[left].ID < proxies[right].ID })
	if _, err := agentruntime.ValidateSnapshot(agentruntime.Snapshot{Revision: 1, Rules: rules, ProxyConfig: proxies, RelayConfig: relays}); err != nil {
		return nil, "", err
	}
	if err := agentruntime.ValidateRelayConfig(relays); err != nil {
		return nil, "", err
	}
	if rules == nil {
		rules = make([]agentruntime.Rule, 0)
	}
	payload, err := json.Marshal(struct {
		ForwardConfig []agentruntime.Rule        `json:"forward_config"`
		ProxyConfig   []agentruntime.ProxyAccess `json:"proxy_config,omitempty"`
		RelayConfig   []agentruntime.RelayConfig `json:"relay_config,omitempty"`
	}{ForwardConfig: rules, ProxyConfig: proxies, RelayConfig: relays})
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(payload)
	return payload, hex.EncodeToString(sum[:]), nil
}

func DecodeConfigSnapshot(payload []byte, now time.Time) (ConfigSnapshot, error) {
	var value ConfigSnapshot
	if err := decodeStrictPayload(payload, &value); err != nil {
		return ConfigSnapshot{}, err
	}
	if value.Revision < 1 || !digestPattern.MatchString(value.SHA256) ||
		value.ValidUntil.IsZero() || !value.ValidUntil.After(now) {
		return ConfigSnapshot{}, errors.New("invalid forward snapshot revision, digest or expiry")
	}
	_, digest, err := CanonicalConfigWithRelay(value.ForwardConfig, value.ProxyConfig, value.RelayConfig)
	if err != nil {
		return ConfigSnapshot{}, fmt.Errorf("invalid forward snapshot: %w", err)
	}
	if digest != value.SHA256 {
		return ConfigSnapshot{}, errors.New("forward snapshot digest mismatch")
	}
	return value, nil
}

func DecodeConfigResult(payload []byte) (ConfigResult, error) {
	var value ConfigResult
	if err := decodeStrictPayload(payload, &value); err != nil {
		return ConfigResult{}, err
	}
	if value.Revision < 1 || !digestPattern.MatchString(value.SHA256) {
		return ConfigResult{}, errors.New("invalid configuration result revision or digest")
	}
	switch value.Status {
	case "applied":
		if value.ErrorCode != "" || value.ErrorMessage != "" {
			return ConfigResult{}, errors.New("applied result cannot include an error")
		}
	case "rejected":
		if !errorCodePattern.MatchString(value.ErrorCode) || len(value.ErrorMessage) > 1024 ||
			!utf8.ValidString(value.ErrorMessage) {
			return ConfigResult{}, errors.New("invalid configuration rejection")
		}
		for _, char := range value.ErrorMessage {
			if unicode.IsControl(char) {
				return ConfigResult{}, errors.New("configuration rejection contains a control character")
			}
		}
	default:
		return ConfigResult{}, errors.New("unknown configuration result status")
	}
	return value, nil
}

func decodeStrictPayload(payload []byte, target any) error {
	if len(payload) > MaxFrameBytes {
		return errors.New("Agent payload exceeds 1 MiB")
	}
	if err := rejectDuplicateEnvelopeFields(payload); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode Agent payload: %w", err)
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return errors.New("Agent payload has trailing JSON")
	}
	return nil
}
