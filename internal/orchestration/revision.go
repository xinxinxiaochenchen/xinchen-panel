package orchestration

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"

	"controlplane/internal/agentruntime"
)

// CanonicalForwardPayload hashes only executable configuration. Revision and
// diagnostics are stored separately, so unchanged rules do not create a new
// desired revision.
func CanonicalForwardPayload(compiled CompiledForwardSnapshot) ([]byte, string, error) {
	rules := append([]agentruntime.Rule(nil), compiled.Snapshot.Rules...)
	for _, rule := range rules {
		if !rule.Enabled {
			return nil, "", errors.New("canonical forward payload contains a disabled rule")
		}
	}
	sort.Slice(rules, func(left, right int) bool { return rules[left].ID < rules[right].ID })
	if _, err := agentruntime.ValidateSnapshot(agentruntime.Snapshot{Revision: compiled.Snapshot.Revision, Rules: rules}); err != nil {
		return nil, "", err
	}
	if rules == nil {
		rules = make([]agentruntime.Rule, 0)
	}
	payload, err := json.Marshal(struct {
		ForwardConfig []agentruntime.Rule `json:"forward_config"`
	}{ForwardConfig: rules})
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(payload)
	return payload, hex.EncodeToString(sum[:]), nil
}
