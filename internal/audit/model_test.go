package audit

import (
	"strings"
	"testing"
	"time"
)

func TestCursorRoundTripAndRejectsMalformedInput(t *testing.T) {
	at := time.Date(2026, 9, 27, 8, 0, 0, 123000000, time.UTC)
	id := "11111111-1111-7111-8111-111111111111"
	cursor := encodeCursor(at, id)
	gotAt, gotID, err := decodeCursor(cursor)
	if err != nil || !gotAt.Equal(at) || gotID != id {
		t.Fatalf("decoded cursor = %s %s %v", gotAt, gotID, err)
	}
	for _, value := range []string{"not-base64", encodeCursor(at, "bad"), strings.Repeat("A", 1024)} {
		if _, _, err := decodeCursor(value); err == nil {
			t.Fatalf("accepted malformed cursor %q", value)
		}
	}
}
