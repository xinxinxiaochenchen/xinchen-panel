package orchestration

import (
	"reflect"
	"strconv"
	"testing"
)

func TestRankHealthyLinesRespectsPriorityAndStableRetryOrder(t *testing.T) {
	input := []LineCandidate{
		{ID: "fallback", Priority: 20, Weight: 100, Healthy: true},
		{ID: "slow", Priority: 10, Weight: 1, Healthy: true},
		{ID: "fast", Priority: 10, Weight: 5, Healthy: true},
		{ID: "offline", Priority: 0, Weight: 100, Healthy: false},
	}
	first, err := RankHealthyLines("connection-123", input)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 3 || first[0].Priority != 10 || first[1].Priority != 10 || first[2].ID != "fallback" {
		t.Fatalf("ranked lines = %+v", first)
	}
	for _, reordered := range [][]LineCandidate{{input[3], input[2], input[0], input[1]}, {input[1], input[0], input[3], input[2]}} {
		again, err := RankHealthyLines("connection-123", reordered)
		if err != nil || !reflect.DeepEqual(first, again) {
			t.Fatalf("unstable ranking = %+v, %v", again, err)
		}
	}
}

func TestRankHealthyLinesWeightsFirstChoice(t *testing.T) {
	input := []LineCandidate{
		{ID: "light", Priority: 10, Weight: 1, Healthy: true},
		{ID: "heavy", Priority: 10, Weight: 3, Healthy: true},
	}
	heavy := 0
	for i := 0; i < 10000; i++ {
		ranked, err := RankHealthyLines(strconv.Itoa(i), input)
		if err != nil {
			t.Fatal(err)
		}
		if ranked[0].ID == "heavy" {
			heavy++
		}
	}
	if heavy < 7200 || heavy > 7800 {
		t.Fatalf("weight 3:1 selected heavy %d/10000 times", heavy)
	}
}

func TestRankHealthyLinesRejectsMalformedCandidates(t *testing.T) {
	valid := []LineCandidate{{ID: "line-1", Priority: 1, Weight: 1, Healthy: true}}
	for _, candidate := range []struct {
		connectionID string
		lines        []LineCandidate
	}{
		{"", valid},
		{"connection", []LineCandidate{{ID: "", Priority: 1, Weight: 1, Healthy: true}}},
		{"connection", []LineCandidate{{ID: "line-1", Priority: 1, Weight: 0, Healthy: true}}},
		{"connection", []LineCandidate{{ID: "line-1", Priority: -1, Weight: 1, Healthy: true}}},
		{"connection", []LineCandidate{{ID: "line-1", Priority: 1001, Weight: 1, Healthy: true}}},
		{"connection", []LineCandidate{{ID: "line-1", Priority: 1, Weight: 101, Healthy: true}}},
		{"connection", append(append([]LineCandidate{}, valid...), valid[0])},
	} {
		if _, err := RankHealthyLines(candidate.connectionID, candidate.lines); err == nil {
			t.Fatalf("accepted malformed ranking input: %+v", candidate)
		}
	}
	if ranked, err := RankHealthyLines("connection", []LineCandidate{{ID: "offline", Healthy: false}}); err != nil || len(ranked) != 0 {
		t.Fatalf("offline-only ranking = %+v, %v", ranked, err)
	}
}

func TestRankHealthyLinesRemovalPreservesSurvivingRetryOrder(t *testing.T) {
	input := []LineCandidate{
		{ID: "a", Priority: 0, Weight: 1, Healthy: true},
		{ID: "b", Priority: 0, Weight: 5, Healthy: true},
		{ID: "c", Priority: 0, Weight: 9, Healthy: true},
		{ID: "d", Priority: 1000, Weight: 100, Healthy: true},
	}
	original := append([]LineCandidate(nil), input...)
	for i := 0; i < 100; i++ {
		connection := strconv.Itoa(i)
		before, err := RankHealthyLines(connection, input)
		if err != nil {
			t.Fatal(err)
		}
		remaining := append([]LineCandidate(nil), input...)
		for index := range remaining {
			if remaining[index].ID == before[0].ID {
				remaining[index].Healthy = false
			}
		}
		after, err := RankHealthyLines(connection, remaining)
		if err != nil || !reflect.DeepEqual(after, before[1:]) {
			t.Fatalf("failure reordered surviving routes: before=%+v after=%+v error=%v", before, after, err)
		}
	}
	if !reflect.DeepEqual(input, original) {
		t.Fatal("ranking modified caller's candidates")
	}
}
