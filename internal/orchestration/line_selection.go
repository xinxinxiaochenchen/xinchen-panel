package orchestration

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"math"
	"slices"
)

// LineCandidate contains the current health and selection policy for one line.
// The caller must set Healthy only after checking every hop and its applied
// configuration; this function does not infer Agent state from an ID.
type LineCandidate struct {
	ID       string
	Priority int
	Weight   int
	Healthy  bool
}

// RankHealthyLines returns a stable weighted order within each priority tier.
// The first line is selected with probability proportional to its weight;
// subsequent entries are the retry order after an unsuccessful connection.
func RankHealthyLines(connectionID string, candidates []LineCandidate) ([]LineCandidate, error) {
	if connectionID == "" {
		return nil, errors.New("connection ID is required")
	}
	type rankedLine struct {
		candidate LineCandidate
		score     float64
	}
	ranked := make([]rankedLine, 0, len(candidates))
	seen := make(map[string]bool, len(candidates))
	for _, candidate := range candidates {
		if !candidate.Healthy {
			continue
		}
		if candidate.ID == "" || candidate.Priority < 0 || candidate.Priority > 1000 || candidate.Weight < 1 || candidate.Weight > 100 || seen[candidate.ID] {
			return nil, errors.New("invalid healthy line candidate")
		}
		seen[candidate.ID] = true
		digest := sha256.Sum256([]byte(connectionID + "\x00" + candidate.ID))
		value := binary.BigEndian.Uint64(digest[:8])
		// (0,1] avoids log(0), including when the digest starts with zero.
		uniform := (float64(value>>11) + 1) / (1 << 53)
		ranked = append(ranked, rankedLine{candidate: candidate, score: -math.Log(uniform) / float64(candidate.Weight)})
	}
	slices.SortFunc(ranked, func(left, right rankedLine) int {
		if left.candidate.Priority != right.candidate.Priority {
			return left.candidate.Priority - right.candidate.Priority
		}
		if left.score < right.score {
			return -1
		}
		if left.score > right.score {
			return 1
		}
		if left.candidate.ID < right.candidate.ID {
			return -1
		}
		if left.candidate.ID > right.candidate.ID {
			return 1
		}
		return 0
	})
	result := make([]LineCandidate, len(ranked))
	for index, line := range ranked {
		result[index] = line.candidate
	}
	return result, nil
}
