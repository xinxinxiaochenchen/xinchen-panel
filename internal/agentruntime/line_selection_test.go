package agentruntime

import "testing"

func TestRankProxyLineCandidatesUsesPriorityWeightAndStableRetryOrder(t *testing.T) {
	candidates := []ProxyLineCandidate{
		{LineID: "line-low", Priority: 20, Weight: 1},
		{LineID: "line-high", Priority: 10, Weight: 3},
		{LineID: "line-peer", Priority: 10, Weight: 1},
	}
	first, err := RankProxyLineCandidates("connection-1", candidates)
	if err != nil {
		t.Fatal(err)
	}
	second, err := RankProxyLineCandidates("connection-1", candidates)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != len(candidates) || len(second) != len(candidates) {
		t.Fatalf("candidate count = %d/%d", len(first), len(second))
	}
	for index := range first {
		if first[index] != second[index] {
			t.Fatalf("unstable retry order: first=%+v second=%+v", first, second)
		}
	}
	if first[0].Priority != 10 || first[1].Priority != 10 || first[2].Priority != 20 {
		t.Fatalf("priority tier was not preserved: %+v", first)
	}
}

func TestRankProxyLineCandidatesRejectsInvalidCandidate(t *testing.T) {
	for _, candidate := range []ProxyLineCandidate{
		{LineID: "", Priority: 1, Weight: 1},
		{LineID: "line", Priority: -1, Weight: 1},
		{LineID: "line", Priority: 1, Weight: 0},
	} {
		if _, err := RankProxyLineCandidates("connection-1", []ProxyLineCandidate{candidate}); err == nil {
			t.Fatalf("candidate %+v was accepted", candidate)
		}
	}
}
