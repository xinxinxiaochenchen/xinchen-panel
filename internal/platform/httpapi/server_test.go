package httpapi

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func testLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestHealthEndpoints(t *testing.T) {
	handler := NewHandler(testLogger())
	for _, path := range []string{"/api/v1/health/live", "/api/v1/health/ready"} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("%s: status = %d", path, recorder.Code)
		}
		if recorder.Header().Get("Content-Type") != "application/json" || recorder.Header().Get("X-Request-ID") == "" {
			t.Fatalf("%s: missing headers: %v", path, recorder.Header())
		}
		if strings.TrimSpace(recorder.Body.String()) != `{"status":"ok"}` {
			t.Fatalf("%s: body = %q", path, recorder.Body.String())
		}
	}
}

func TestNotFoundUsesErrorEnvelope(t *testing.T) {
	recorder := httptest.NewRecorder()
	NewHandler(testLogger()).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/missing", nil))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d", recorder.Code)
	}
	var body struct {
		Error struct {
			Code      string `json:"code"`
			RequestID string `json:"request_id"`
		} `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error.Code != "NOT_FOUND" || body.Error.RequestID == "" || body.Error.RequestID != recorder.Header().Get("X-Request-ID") {
		t.Fatalf("body = %s", recorder.Body.String())
	}
}

func TestPanicRecoveryDoesNotLeakDetails(t *testing.T) {
	handler := withMiddleware(testLogger(), http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("private panic detail")
	}))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/panic", nil))
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), `"code":"INTERNAL"`) || strings.Contains(recorder.Body.String(), "private panic detail") {
		t.Fatalf("body = %s", recorder.Body.String())
	}
}
