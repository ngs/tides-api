package http

import (
	nethttp "net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	csvstore "go.ngs.io/tides-api/internal/adapter/store/csv"
	"go.ngs.io/tides-api/internal/usecase"
)

const (
	testCSVContent = "constituent,amplitude_m,phase_deg\nM2,0.5,30.0\n"
	testStart      = "2025-10-27T00:00:00Z"
	testEnd        = "2025-10-28T00:00:00Z"
	testLatStr     = "35.6"
	testLonStr     = "139.7"
)

// newTestEnv builds a prediction use case backed by a real CSV store rooted
// at a temp directory. Layout:
//
//	<base>/data/mock_tokyo_constituents.csv          (legitimate station)
//	<base>/outside/secret_constituents.csv           (reachable ONLY via path traversal)
//
// It returns the use case and the base directory.
func newTestEnv(t *testing.T) (*usecase.PredictionUseCase, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	base := t.TempDir()
	dataDir := filepath.Join(base, "data")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatalf("mkdir dataDir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "mock_tokyo_constituents.csv"), []byte(testCSVContent), 0o644); err != nil {
		t.Fatalf("write station csv: %v", err)
	}

	// A subdirectory matching the "mock_*" naming scheme. Its presence lets a
	// traversal like station_id="x/../../outside/secret" resolve through an
	// existing path component, demonstrating the actual escape.
	if err := os.MkdirAll(filepath.Join(dataDir, "mock_x"), 0o755); err != nil {
		t.Fatalf("mkdir mock_x: %v", err)
	}

	outsideDir := filepath.Join(base, "outside")
	if err := os.MkdirAll(outsideDir, 0o755); err != nil {
		t.Fatalf("mkdir outsideDir: %v", err)
	}
	// This file mimics sensitive data outside the configured data directory.
	if err := os.WriteFile(filepath.Join(outsideDir, "secret_constituents.csv"), []byte(testCSVContent), 0o644); err != nil {
		t.Fatalf("write outside csv: %v", err)
	}

	store := csvstore.NewConstituentStore(dataDir)
	uc := usecase.NewPredictionUseCase(store, store, nil)
	return uc, base
}

func newTestRouter(t *testing.T) *gin.Engine {
	t.Helper()
	uc, _ := newTestEnv(t)
	h := NewHandler(uc)
	r := gin.New()
	r.GET("/v1/tides/predictions", h.GetPredictions)
	return r
}

func doGet(t *testing.T, r *gin.Engine, rawQuery string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), nethttp.MethodGet, "/v1/tides/predictions?"+rawQuery, nethttp.NoBody)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// Regression test for: station_id is passed unvalidated into a filesystem path
// (handler.go GetPredictions -> csv/loader.go LoadForStation), allowing path
// traversal via "..", "/" and percent-encoded dots. Requests with such
// station_id values must be rejected with 400 before any file access; in
// particular a traversal that resolves to a real file outside the data
// directory must NOT succeed.
func TestGetPredictions_StationIDPathTraversalRejected(t *testing.T) {
	r := newTestRouter(t)

	timeParams := url.Values{
		paramStart: {testStart},
		paramEnd:   {testEnd},
	}

	cases := []struct {
		name      string
		stationID string
	}{
		// Resolves to <base>/outside/secret_constituents.csv, a valid CSV
		// outside the data dir. Currently this succeeds (200), proving the
		// traversal reaches file access. It must be a 400.
		{"traversal to existing file outside data dir", "x/../../outside/secret"},
		{"traversal towards /etc/passwd", "x/../../../../../../etc/passwd"},
		{"slash and dot-dot within data dir", "tokyo/../tokyo"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			q := url.Values{paramStationID: {tc.stationID}}
			for k, v := range timeParams {
				q[k] = v
			}
			w := doGet(t, r, q.Encode())
			if w.Code != nethttp.StatusBadRequest {
				t.Errorf("station_id=%q: expected 400 Bad Request, got %d (body: %s)",
					tc.stationID, w.Code, w.Body.String())
			}
		})
	}

	t.Run("percent-encoded traversal (%2e)", func(t *testing.T) {
		// Raw query string; gin decodes %2e/%2f when parsing the query,
		// so this is equivalent to "../../outside/secret" server-side.
		raw := "station_id=x%2f%2e%2e%2f%2e%2e%2foutside%2fsecret&start=2025-10-27T00%3A00%3A00Z&end=2025-10-28T00%3A00%3A00Z"
		w := doGet(t, r, raw)
		if w.Code != nethttp.StatusBadRequest {
			t.Errorf("percent-encoded traversal: expected 400 Bad Request, got %d (body: %s)", w.Code, w.Body.String())
		}
	})
}

// Regression test for: handler.go returns every use-case error as 400 and
// echoes err.Error() to the client (handler.go:140-143). A store-level
// internal failure (CSV open failure for an unknown station) must be mapped
// to 500 (or 404), and the response body must not leak internal file paths
// such as the data directory or ".csv" filenames.
func TestGetPredictions_InternalErrorNotExposedAsBadRequest(t *testing.T) {
	uc, base := newTestEnv(t)
	h := NewHandler(uc)
	r := gin.New()
	r.GET("/v1/tides/predictions", h.GetPredictions)

	q := url.Values{
		paramStationID: {"nosuchstation"},
		paramStart:     {testStart},
		paramEnd:       {testEnd},
	}
	w := doGet(t, r, q.Encode())

	if w.Code == nethttp.StatusBadRequest {
		t.Errorf("store-level failure for unknown station must not be 400; got %d (body: %s)",
			w.Code, w.Body.String())
	}
	if w.Code != nethttp.StatusInternalServerError && w.Code != nethttp.StatusNotFound {
		t.Errorf("expected 500 or 404 for unknown station, got %d (body: %s)", w.Code, w.Body.String())
	}

	body := w.Body.String()
	if strings.Contains(body, ".csv") {
		t.Errorf("response body leaks internal file name (.csv): %s", body)
	}
	if strings.Contains(body, base) {
		t.Errorf("response body leaks internal file path (%s): %s", base, body)
	}
	if strings.Contains(body, "data/") || strings.Contains(body, "failed to open") {
		t.Errorf("response body leaks internal storage details: %s", body)
	}
}

// Regression test for: providing only lat (without lon) is silently ignored
// because handler.go:54 requires BOTH lat and lon to be non-empty before
// parsing. The request must fail with 400 and an error message that
// specifically points at the missing lon parameter (not a generic
// "either lat/lon or station_id" / "start parameter is required" message).
func TestGetPredictions_LatWithoutLonRejectedExplicitly(t *testing.T) {
	r := newTestRouter(t)

	t.Run("lat only with start/end", func(t *testing.T) {
		q := url.Values{
			paramLat:   {testLatStr},
			paramStart: {testStart},
			paramEnd:   {testEnd},
		}
		w := doGet(t, r, q.Encode())
		body := w.Body.String()

		if w.Code != nethttp.StatusBadRequest {
			t.Fatalf("expected 400 Bad Request, got %d (body: %s)", w.Code, body)
		}
		if !strings.Contains(body, "lon") {
			t.Errorf("error message must mention missing lon; got: %s", body)
		}
		// The message must be specific to the missing lon, not the generic
		// mutual-exclusion message that mentions station_id.
		if strings.Contains(body, "station_id") {
			t.Errorf("error message should be specific about missing lon, not the generic lat/lon-or-station_id message; got: %s", body)
		}
	})

	t.Run("lat only without start/end", func(t *testing.T) {
		q := url.Values{paramLat: {testLatStr}}
		w := doGet(t, r, q.Encode())
		body := w.Body.String()

		if w.Code != nethttp.StatusBadRequest {
			t.Fatalf("expected 400 Bad Request, got %d (body: %s)", w.Code, body)
		}
		if !strings.Contains(body, "lon") {
			t.Errorf("error message must mention missing lon; got: %s", body)
		}
	})
}

// The datum query parameter must be honoured end-to-end: a valid datum=CD
// request succeeds and reports the applied datum, while an unsupported value is
// rejected with 400 before any computation.
func TestGetPredictions_DatumParameter(t *testing.T) {
	r := newTestRouter(t)

	t.Run("datum=CD succeeds and echoes applied datum", func(t *testing.T) {
		q := url.Values{
			paramStationID: {testStation},
			paramStart:     {testStart},
			paramEnd:       {testEnd},
			"datum":        {"CD"},
		}
		w := doGet(t, r, q.Encode())
		if w.Code != nethttp.StatusOK {
			t.Fatalf("expected 200 OK, got %d (body: %s)", w.Code, w.Body.String())
		}
		body := w.Body.String()
		if !strings.Contains(body, `"datum":"CD"`) {
			t.Errorf(`response must report datum "CD"; got: %s`, body)
		}
		if !strings.Contains(body, `"chart_datum_offset_m"`) {
			t.Errorf("response must include chart_datum_offset_m; got: %s", body)
		}
	})

	t.Run("unsupported datum is 400", func(t *testing.T) {
		q := url.Values{
			paramStationID: {testStation},
			paramStart:     {testStart},
			paramEnd:       {testEnd},
			"datum":        {"LAT"},
		}
		w := doGet(t, r, q.Encode())
		if w.Code != nethttp.StatusBadRequest {
			t.Errorf("expected 400 Bad Request for unsupported datum, got %d (body: %s)", w.Code, w.Body.String())
		}
	})
}
