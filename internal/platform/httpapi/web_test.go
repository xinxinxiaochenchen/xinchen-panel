package httpapi

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestWebHandlerPreservesAPIAndServesPreview(t *testing.T) {
	assets := fstest.MapFS{
		"index.html":     &fstest.MapFile{Data: []byte("<html>preview</html>")},
		"assets/app.js":  &fstest.MapFile{Data: []byte("console.log('ready')")},
		"assets/app.css": &fstest.MapFile{Data: []byte("body{color:red}")},
	}
	api := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"source":"api"}`))
	})
	handler := NewWebHandler(api, fs.FS(assets))
	for name, input := range map[string]struct {
		method, path string
		status       int
		body         string
	}{
		"API":           {http.MethodGet, "/api/v1/health/live", http.StatusAccepted, `"source":"api"`},
		"home":          {http.MethodGet, "/", http.StatusOK, "preview"},
		"module route":  {http.MethodGet, "/nodes", http.StatusOK, "preview"},
		"script":        {http.MethodGet, "/assets/app.js", http.StatusOK, "console.log"},
		"missing asset": {http.MethodGet, "/assets/absent.js", http.StatusNotFound, ""},
		"write static":  {http.MethodPost, "/", http.StatusMethodNotAllowed, ""},
	} {
		t.Run(name, func(t *testing.T) {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(input.method, input.path, nil))
			if response.Code != input.status || !strings.Contains(response.Body.String(), input.body) {
				t.Fatalf("%s %s = %d %q", input.method, input.path, response.Code, response.Body.String())
			}
			if name == "script" && !strings.HasPrefix(response.Header().Get("Content-Type"), "text/javascript") {
				t.Fatalf("script MIME type = %q", response.Header().Get("Content-Type"))
			}
		})
	}
}
