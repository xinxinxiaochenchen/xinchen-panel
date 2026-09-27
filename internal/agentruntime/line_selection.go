package agentruntime

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"math"
	"slices"
)

// ProxyLineCandidate describes one line that an already authenticated proxy
// access may try. The caller must only pass locally healthy candidates.
type ProxyLineCandidate struct {
	LineID          string `json:"line_id"`
	RelayGeneration uint64 `json:"relay_generation,omitempty"`
	Priority        int    `json:"priority"`
	Weight          int    `json:"weight"`
}

// RankProxyLineCandidates returns a deterministic retry order. The first
// priority tier is selected before lower tiers; within a tier, a weighted
// exponential-race score makes larger weights more likely to appear first.
func RankProxyLineCandidates(connectionID string, candidates []ProxyLineCandidate) ([]ProxyLineCandidate, error) {
	if connectionID == "" {
		return nil, errors.New("connection ID is required")
	}
	type ranked struct {
		candidate ProxyLineCandidate
		score     float64
	}
	items := make([]ranked, 0, len(candidates))
	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		if candidate.LineID == "" || candidate.Priority < 0 || candidate.Priority > 1000 || candidate.Weight < 1 || candidate.Weight > 100 {
			return nil, errors.New("invalid proxy line candidate")
		}
		if _, ok := seen[candidate.LineID]; ok {
			return nil, errors.New("duplicate proxy line candidate")
		}
		seen[candidate.LineID] = struct{}{}
		digest := sha256.Sum256([]byte(connectionID + "\x00" + candidate.LineID))
		value := binary.BigEndian.Uint64(digest[:8])
		uniform := (float64(value>>11) + 1) / (1 << 53)
		items = append(items, ranked{candidate: candidate, score: -math.Log(uniform) / float64(candidate.Weight)})
	}
	slices.SortFunc(items, func(left, right ranked) int {
		if left.candidate.Priority != right.candidate.Priority {
			return left.candidate.Priority - right.candidate.Priority
		}
		if left.score < right.score {
			return -1
		}
		if left.score > right.score {
			return 1
		}
		if left.candidate.LineID < right.candidate.LineID {
			return -1
		}
		if left.candidate.LineID > right.candidate.LineID {
			return 1
		}
		return 0
	})
	result := make([]ProxyLineCandidate, len(items))
	for index, item := range items {
		result[index] = item.candidate
	}
	return result, nil
}
