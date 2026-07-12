package http

import (
	"encoding/json"
	nethttp "net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

const testStation = "tokyo"

func newParametersTestRouter(t *testing.T) *gin.Engine {
	t.Helper()
	uc, _ := newTestEnv(t)
	h := NewHandler(uc)
	r := gin.New()
	r.GET("/v1/tides/parameters", h.GetTideParameters)
	return r
}

func doGetParameters(t *testing.T, r *gin.Engine, rawQuery string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), nethttp.MethodGet, "/v1/tides/parameters?"+rawQuery, nethttp.NoBody)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// A valid station query must return the harmonic constants for the station.
func TestGetTideParameters_Station(t *testing.T) {
	r := newParametersTestRouter(t)

	w := doGetParameters(t, r, url.Values{paramStationID: {testStation}}.Encode())
	if w.Code != nethttp.StatusOK {
		t.Fatalf("expected 200, got %d (body: %s)", w.Code, w.Body.String())
	}

	var resp struct {
		StationID     string `json:"station_id"`
		Source        string `json:"source"`
		Datum         string `json:"datum"`
		ReferenceTime string `json:"reference_time"`
		Constituents  []struct {
			Name                   string  `json:"name"`
			SpeedDegPerHr          float64 `json:"speed_deg_per_hr"`
			AmplitudeM             float64 `json:"amplitude_m"`
			PhaseDeg               float64 `json:"phase_deg"`
			EquilibriumArgumentDeg float64 `json:"equilibrium_argument_deg"`
		} `json:"constituents"`
		Meta map[string]string `json:"meta"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v (body: %s)", err, w.Body.String())
	}

	if resp.StationID != "tokyo" {
		t.Errorf("station_id = %q, want tokyo", resp.StationID)
	}
	if resp.Source != "csv" {
		t.Errorf("source = %q, want csv", resp.Source)
	}
	if resp.Datum != "MSL" {
		t.Errorf("datum = %q, want MSL", resp.Datum)
	}
	if resp.ReferenceTime == "" {
		t.Error("reference_time must be set")
	}
	if len(resp.Constituents) != 1 {
		t.Fatalf("expected 1 constituent, got %d (body: %s)", len(resp.Constituents), w.Body.String())
	}
	c := resp.Constituents[0]
	if c.Name != "M2" || c.AmplitudeM != 0.5 || c.PhaseDeg != 30.0 {
		t.Errorf("constituent = %+v, want M2 amplitude 0.5 phase 30", c)
	}
	if c.SpeedDegPerHr == 0 {
		t.Error("speed_deg_per_hr must be set")
	}
	if resp.Meta["attribution"] == "" {
		t.Error("meta.attribution must be set")
	}

	// A station response must not include a lat/lon location object.
	if strings.Contains(w.Body.String(), `"location"`) {
		t.Errorf("station response must not include location: %s", w.Body.String())
	}
}

// Invalid query parameter combinations must return 400 without touching
// storage, matching the predictions endpoint validation.
func TestGetTideParameters_BadRequest(t *testing.T) {
	r := newParametersTestRouter(t)

	cases := []struct {
		name     string
		rawQuery string
		wantIn   string
	}{
		{"no parameters", "", "either lat/lon or station_id"},
		{"lat without lon", url.Values{paramLat: {testLatStr}}.Encode(), "lon"},
		{"lon without lat", url.Values{paramLon: {testLonStr}}.Encode(), "lat"},
		{"invalid latitude", url.Values{paramLat: {"abc"}, paramLon: {testLonStr}}.Encode(), "latitude"},
		// strconv.ParseFloat accepts "NaN"/"Inf", so these reach validation as
		// non-finite floats and must be rejected rather than silently used.
		{"NaN latitude", url.Values{paramLat: {"NaN"}, paramLon: {testLonStr}}.Encode(), "latitude"},
		{"Inf longitude", url.Values{paramLat: {testLatStr}, paramLon: {"Inf"}}.Encode(), "longitude"},
		{"path traversal station_id", url.Values{paramStationID: {"x/../../outside/secret"}}.Encode(), "station_id"},
		{"lat/lon and station_id", url.Values{
			paramLat: {testLatStr}, paramLon: {testLonStr}, paramStationID: {testStation},
		}.Encode(), "mutually exclusive"},
		{"station_id with fes source", url.Values{
			paramStationID: {testStation}, "source": {"fes"},
		}.Encode(), "FES source"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := doGetParameters(t, r, tc.rawQuery)
			if w.Code != nethttp.StatusBadRequest {
				t.Errorf("expected 400, got %d (body: %s)", w.Code, w.Body.String())
			}
			if !strings.Contains(w.Body.String(), tc.wantIn) {
				t.Errorf("body must mention %q; got: %s", tc.wantIn, w.Body.String())
			}
		})
	}
}

// An unknown station must return 404 without leaking internal file paths.
func TestGetTideParameters_UnknownStationNotFound(t *testing.T) {
	uc, base := newTestEnv(t)
	h := NewHandler(uc)
	r := gin.New()
	r.GET("/v1/tides/parameters", h.GetTideParameters)

	w := doGetParameters(t, r, url.Values{paramStationID: {"nosuchstation"}}.Encode())
	if w.Code != nethttp.StatusNotFound {
		t.Fatalf("expected 404, got %d (body: %s)", w.Code, w.Body.String())
	}

	body := w.Body.String()
	if strings.Contains(body, ".csv") || strings.Contains(body, base) {
		t.Errorf("response body leaks internal storage details: %s", body)
	}
}
