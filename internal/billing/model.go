package billing

import (
	"errors"
	"time"
)

var (
	ErrNotFound = errors.New("billing resource not found")
	ErrConflict = errors.New("billing resource conflict")
	ErrSequence = errors.New("usage report sequence gap")
)

type Period struct {
	ID              string
	MembershipID    string
	UserID          string
	StartsAt        time.Time
	EndsAt          time.Time
	QuotaBytes      int64
	UploadedBytes   int64
	DownloadedBytes int64
	ChargedBytes    int64
	Status          string
}

type Connection struct {
	ID              string
	PeriodID        string
	AgentID         string
	IngressNodeID   string
	LineID          string
	MultiplierMilli int64
	StartedAt       time.Time
}

type UsageReport struct {
	ConnectionID string
	Sequence     int64
	Counters
	ObservedAt time.Time
}

type UsageEvent struct {
	ID              string
	ConnectionID    string
	Sequence        int64
	Cumulative      Counters
	UploadedBytes   int64
	DownloadedBytes int64
	ChargedBytes    int64
}
