package orchestration

import (
	"controlplane/internal/agentproto"
	"controlplane/internal/agentruntime"
)

// CanonicalForwardPayload hashes only executable configuration. Revision and
// diagnostics are stored separately, so unchanged rules do not create a new
// desired revision.
func CanonicalForwardPayload(compiled CompiledForwardSnapshot) ([]byte, string, error) {
	if _, err := agentruntime.ValidateSnapshot(compiled.Snapshot); err != nil {
		return nil, "", err
	}
	return agentproto.CanonicalConfigWithRelay(compiled.Snapshot.Rules, compiled.Snapshot.ProxyConfig, compiled.Snapshot.RelayConfig)
}
