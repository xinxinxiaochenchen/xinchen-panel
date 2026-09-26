package audit

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strings"
	"time"
)

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

type Record struct {
	ID          string    `json:"id"`
	ActorUserID string    `json:"actor_user_id"`
	ActorEmail  string    `json:"actor_email"`
	Action      string    `json:"action"`
	ObjectType  string    `json:"object_type"`
	ObjectID    string    `json:"object_id"`
	RequestID   string    `json:"request_id"`
	CreatedAt   time.Time `json:"created_at"`
}

type Page struct {
	Items      []Record `json:"items"`
	NextCursor *string  `json:"next_cursor"`
}

type Cursor struct {
	CreatedAt time.Time
	ID        string
}

func encodeCursor(createdAt time.Time, id string) string {
	payload, _ := json.Marshal(struct {
		CreatedAt time.Time `json:"created_at"`
		ID        string    `json:"id"`
	}{createdAt.UTC(), id})
	return base64.RawURLEncoding.EncodeToString(payload)
}

func decodeCursor(value string) (time.Time, string, error) {
	if value == "" || len(value) > 512 {
		return time.Time{}, "", errors.New("invalid audit cursor")
	}
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return time.Time{}, "", errors.New("invalid audit cursor")
	}
	var payload struct {
		CreatedAt time.Time `json:"created_at"`
		ID        string    `json:"id"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil || payload.CreatedAt.IsZero() || !uuidPattern.MatchString(payload.ID) {
		return time.Time{}, "", errors.New("invalid audit cursor")
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return time.Time{}, "", errors.New("invalid audit cursor")
	}
	return payload.CreatedAt.UTC(), strings.ToLower(payload.ID), nil
}

func CursorFor(record Record) string { return encodeCursor(record.CreatedAt, record.ID) }

func ParseCursor(value string) (Cursor, error) {
	createdAt, id, err := decodeCursor(value)
	if err != nil {
		return Cursor{}, err
	}
	return Cursor{CreatedAt: createdAt, ID: id}, nil
}
