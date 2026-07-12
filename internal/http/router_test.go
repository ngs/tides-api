package http

import (
	nethttp "net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// Regression test for: SetupRouter splits CORS_ALLOWED_ORIGINS on "," without
// trimming whitespace (router.go:23-28), so "https://a.com, https://b.com"
// yields the entry " https://b.com" and requests from https://b.com are not
// allowed (or the CORS middleware rejects the config entirely).
func TestSetupRouter_CORSAllowedOriginsTrimmed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("CORS_ALLOWED_ORIGINS", "https://a.com, https://b.com")

	uc, _ := newTestEnv(t)

	var router *gin.Engine
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("SetupRouter panicked with space-padded CORS origins (origins must be trimmed): %v", r)
			}
		}()
		router = SetupRouter(uc)
	}()

	check := func(t *testing.T, origin string) {
		t.Helper()

		// Preflight request.
		req := httptest.NewRequestWithContext(t.Context(), nethttp.MethodOptions, "/health", nethttp.NoBody)
		req.Header.Set("Origin", origin)
		req.Header.Set("Access-Control-Request-Method", nethttp.MethodGet)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		if got := w.Header().Get("Access-Control-Allow-Origin"); got != origin {
			t.Errorf("preflight from %s: Access-Control-Allow-Origin = %q, want %q (status %d)",
				origin, got, origin, w.Code)
		}

		// Simple GET request.
		req = httptest.NewRequestWithContext(t.Context(), nethttp.MethodGet, "/health", nethttp.NoBody)
		req.Header.Set("Origin", origin)
		w = httptest.NewRecorder()
		router.ServeHTTP(w, req)

		if got := w.Header().Get("Access-Control-Allow-Origin"); got != origin {
			t.Errorf("GET from %s: Access-Control-Allow-Origin = %q, want %q (status %d)",
				origin, got, origin, w.Code)
		}
	}

	t.Run("first origin", func(t *testing.T) { check(t, "https://a.com") })
	t.Run("second origin after comma+space", func(t *testing.T) { check(t, "https://b.com") })
}
