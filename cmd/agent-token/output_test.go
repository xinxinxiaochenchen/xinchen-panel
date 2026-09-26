package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"controlplane/internal/agentidentity"
)

func TestWriteTokenOutputCreatesPrivateFileWithoutOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "enrollment-token.json")
	token := agentidentity.EnrollmentToken{Token: "secret-token", ExpiresAt: time.Now().Add(time.Minute)}
	if err := WriteTokenOutput(path, token); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
		t.Fatalf("token output mode = %v, %v", info, err)
	}
	contents, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(contents), "secret-token") {
		t.Fatalf("token file = %v, %v", string(contents), err)
	}
	if err := WriteTokenOutput(path, token); err == nil {
		t.Fatal("existing token file overwritten")
	}
	contents, err = os.ReadFile(path)
	if err != nil || !strings.Contains(string(contents), "secret-token") {
		t.Fatal("existing token file was removed")
	}
}

func TestReserveTokenOutputRejectsExistingPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reserved-token.json")
	file, err := ReserveTokenOutput(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := ReserveTokenOutput(path); err == nil {
		t.Fatal("existing token output was reserved twice")
	}
	if err := file.Write(agentidentity.EnrollmentToken{Token: "secret", ExpiresAt: time.Now().Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
}
