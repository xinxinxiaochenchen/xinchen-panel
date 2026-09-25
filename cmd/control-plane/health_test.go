package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCheckLocalHealthAcceptsReadyService(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/health/ready" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	address := strings.TrimPrefix(server.URL, "http://")
	if err := checkLocalHealth(context.Background(), address); err != nil {
		t.Fatal(err)
	}
}

func TestCheckLocalHealthRejectsUnreadyService(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	address := strings.TrimPrefix(server.URL, "http://")
	if err := checkLocalHealth(context.Background(), address); err == nil {
		t.Fatal("expected unready error")
	}
}
