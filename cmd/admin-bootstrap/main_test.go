package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"controlplane/internal/identity"
)

type bootstrapStub struct {
	configured      bool
	err             error
	created         int
	email, password string
}

func (s *bootstrapStub) HasAdmin(context.Context) (bool, error) { return s.configured, s.err }
func (s *bootstrapStub) Create(_ context.Context, email, password string, onlyIfNeeded bool) (identity.PublicUser, bool, error) {
	if s.err != nil {
		return identity.PublicUser{}, false, s.err
	}
	if onlyIfNeeded && s.configured {
		return identity.PublicUser{}, false, nil
	}
	s.created++
	s.email, s.password = email, password
	s.configured = true
	return identity.PublicUser{ID: "admin-id", Email: email}, true, nil
}

type unreadableInput struct{}

func (unreadableInput) Read([]byte) (int, error) { return 0, errors.New("stdin must not be read") }

func TestBootstrapStatusDoesNotReadPasswordOrRequireEmail(t *testing.T) {
	for _, configured := range []bool{false, true} {
		store := &bootstrapStub{configured: configured}
		var out bytes.Buffer
		if err := executeBootstrap(context.Background(), "--status", "", unreadableInput{}, &out, store); err != nil {
			t.Fatal(err)
		}
		want := "empty\n"
		if configured {
			want = "configured\n"
		}
		if out.String() != want || store.created != 0 {
			t.Fatalf("output=%q creates=%d", out.String(), store.created)
		}
	}
}

func TestBootstrapIfNeededPreservesExistingAdministrator(t *testing.T) {
	store := &bootstrapStub{configured: true}
	var out bytes.Buffer
	if err := executeBootstrap(context.Background(), "--if-needed", "", unreadableInput{}, &out, store); err != nil {
		t.Fatal(err)
	}
	if store.created != 0 {
		t.Fatal("existing administrator was replaced")
	}
}

func TestBootstrapPasswordOnlyComesFromBoundedStdin(t *testing.T) {
	store := &bootstrapStub{}
	var out bytes.Buffer
	password := "initial-long-password"
	if err := executeBootstrap(context.Background(), "--if-needed", "owner@example.test", strings.NewReader(password+"\n"), &out, store); err != nil {
		t.Fatal(err)
	}
	if store.created != 1 || store.email != "owner@example.test" || store.password != password {
		t.Fatalf("unexpected input: creates=%d", store.created)
	}
	if strings.Contains(out.String(), password) {
		t.Fatal("password leaked in output")
	}
	for _, bad := range []string{"short\n", strings.Repeat("p", 73), "initial-long-password\nextra-line"} {
		store = &bootstrapStub{}
		if err := executeBootstrap(context.Background(), "--if-needed", "owner@example.test", strings.NewReader(bad), io.Discard, store); err == nil || store.created != 0 {
			t.Fatal("invalid password input reached storage")
		}
	}
}

func TestBootstrapStateErrorDoesNotCreateAdmin(t *testing.T) {
	store := &bootstrapStub{err: errors.New("database unavailable")}
	if err := executeBootstrap(context.Background(), "--if-needed", "owner@example.test", unreadableInput{}, io.Discard, store); err == nil || store.created != 0 {
		t.Fatal("state failure was ignored")
	}
}

func TestBootstrapRejectsUnknownOptions(t *testing.T) {
	for _, args := range [][]string{{"--unknown"}, {"--status", "extra"}} {
		if _, err := bootstrapAction(args); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}
