package subscription

import (
	"context"
	"controlplane/internal/proxyaccess"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestNormalizeSubscription(t *testing.T) {
	id := "01900000-0000-7000-8000-000000000001"
	got, err := Normalize(NewSubscription{Name: "  Laptop  ", ProxyAccessIDs: []string{id}})
	if err != nil || got.Name != "Laptop" || !got.Enabled || got.NameTemplate != "{region} · {name}" {
		t.Fatalf("normalized=%+v err=%v", got, err)
	}
	for _, in := range []NewSubscription{
		{Name: "x"}, {Name: "x", ProxyAccessIDs: []string{id, id}},
		{Name: "x", ProxyAccessIDs: []string{"not-uuid"}},
		{Name: "x", NameTemplate: "{unknown}", ProxyAccessIDs: []string{id}},
		{Name: "x\n", ProxyAccessIDs: []string{id}},
	} {
		if _, err := Normalize(in); err == nil {
			t.Fatalf("accepted %+v", in)
		}
	}
}

func TestSubscriptionTokenIsCanonicalAndBoundToContext(t *testing.T) {
	a, err := GenerateToken()
	if err != nil {
		t.Fatal(err)
	}
	b, err := GenerateToken()
	if err != nil {
		t.Fatal(err)
	}
	if len(a) != 43 || a == b {
		t.Fatal("invalid token entropy")
	}
	h, err := TokenHash(a)
	if err != nil || len(h) != 64 || h == a {
		t.Fatal("invalid token hash")
	}
	for _, bad := range []string{"", a + "=", strings.Repeat("!", 43), " " + a} {
		if _, err := TokenHash(bad); err == nil {
			t.Fatal("accepted noncanonical token")
		}
	}
	cipher, err := proxyaccess.NewCredentialCipher(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := sealToken(cipher, "sub-id", "owner", a)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := openToken(cipher, "sub-id", "owner", sealed); err != nil || got != a {
		t.Fatal("token round trip failed")
	}
	if _, err := openToken(cipher, "sub-id", "other", sealed); err == nil {
		t.Fatal("token changed owner")
	}
	if _, err := cipher.Open("sub-id", "owner", sealed); err == nil {
		t.Fatal("token reused proxy credential context")
	}
	dto, _ := json.Marshal(Subscription{ID: "sub-id", Name: "Laptop"})
	if strings.Contains(string(dto), "token") {
		t.Fatal("metadata leaks token material")
	}
}

func TestStoredSubscriptionMetadataAllowsCascadedEmptyTargets(t *testing.T) {
	input := NewSubscription{Name: "Empty", NameTemplate: "{name}"}
	if _, err := normalize(input, true); err != nil {
		t.Fatalf("stored subscription metadata: %v", err)
	}
	if _, err := Normalize(input); err == nil {
		t.Fatal("new empty subscription accepted")
	}
}

func TestRejectNonCanonicalUUIDAliasesBeforeUsingTokenContext(t *testing.T) {
	repo := NewPostgresRepository(nil, nil)
	id := "01900000-0000-7000-8000-000000000001"
	for _, bad := range []string{strings.ReplaceAll(id, "-", ""), "{" + id + "}"} {
		if _, err := repo.RotateOwn(context.Background(), id, bad, ""); !errors.Is(err, ErrNotFound) {
			t.Fatalf("alias rotation=%v", err)
		}
		if _, err := repo.RevealOwn(context.Background(), id, bad); !errors.Is(err, ErrNotFound) {
			t.Fatalf("alias reveal=%v", err)
		}
	}
}
