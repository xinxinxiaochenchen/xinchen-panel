package id

import (
	"encoding/hex"
	"strings"
	"testing"
)

func TestNewV7VersionVariantAndUniqueness(t *testing.T) {
	first, err := NewV7()
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewV7()
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("UUID collision")
	}
	decoded, err := hex.DecodeString(strings.ReplaceAll(first, "-", ""))
	if err != nil || len(decoded) != 16 {
		t.Fatalf("invalid UUID: %q, %v", first, err)
	}
	if decoded[6]>>4 != 7 || decoded[8]>>6 != 2 {
		t.Fatalf("invalid version or variant: %q", first)
	}
}
