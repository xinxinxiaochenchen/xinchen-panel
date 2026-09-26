package agentclient

import "testing"

func TestRejectsSameRevisionWithChangedDigest(t *testing.T) {
	if err := validateReceivedRevision(2, "abc", 2, "def"); err == nil {
		t.Fatal("same revision with different digest accepted")
	}
	if err := validateReceivedRevision(2, "abc", 1, "abc"); err == nil {
		t.Fatal("stale revision accepted")
	}
	if err := validateReceivedRevision(2, "abc", 2, "abc"); err != nil {
		t.Fatal(err)
	}
	if err := validateReceivedRevision(2, "abc", 3, "def"); err != nil {
		t.Fatal(err)
	}
}
