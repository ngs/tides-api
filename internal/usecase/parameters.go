package usecase

import (
	"fmt"
	"time"

	"go.ngs.io/tides-api/internal/domain"
)

// ParametersRequest encapsulates a harmonic parameters request. Location
// resolution follows the same rules as PredictionRequest: either Lat/Lon or
// StationID must be provided (mutually exclusive), and Source optionally
// forces "csv" or "fes".
type ParametersRequest struct {
	// Location parameters (mutually exclusive with StationID).
	Lat *float64
	Lon *float64

	// Station ID (mutually exclusive with Lat/Lon).
	StationID *string

	// Optional source selector: "csv" or "fes" - if empty, auto-detect.
	Source string
}

// Validate checks if the request is valid.
func (r *ParametersRequest) Validate() error {
	return validateLocation(r.Lat, r.Lon, r.StationID)
}

// LocationPoint is the echoed query location in a parameters response.
type LocationPoint struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
}

// ConstituentParameter is one harmonic constituent in a parameters response.
type ConstituentParameter struct {
	Name                   string  `json:"name"`
	SpeedDegPerHr          float64 `json:"speed_deg_per_hr"`
	AmplitudeM             float64 `json:"amplitude_m"`
	PhaseDeg               float64 `json:"phase_deg"`
	EquilibriumArgumentDeg float64 `json:"equilibrium_argument_deg"`
}

// ParametersResponse contains the harmonic constants for a location or
// station so that clients can compute tide heights locally. See GetParameters
// for the computation contract.
type ParametersResponse struct {
	Location      *LocationPoint         `json:"location,omitempty"`
	StationID     *string                `json:"station_id,omitempty"`
	Source        string                 `json:"source"`
	Datum         string                 `json:"datum"`
	MSL           float64                `json:"msl_m"`
	SeabedDepth   *float64               `json:"seabed_depth_m,omitempty"`
	ReferenceTime string                 `json:"reference_time"`
	Constituents  []ConstituentParameter `json:"constituents"`
	Meta          map[string]string      `json:"meta"`
}

// GetParameters returns the harmonic constants used for tide prediction at a
// location or station, allowing clients to compute tide heights locally.
//
// Computation contract - a client reconstructs the tide height as
//
//	h(t) = msl_m + Σ f_k(t) · A_k · cos(ω_k·Δt + V_k + u_k(t) − φ_k)
//
// where, per constituent k:
//   - A_k is amplitude_m and φ_k is phase_deg (Greenwich phase lag),
//   - ω_k is speed_deg_per_hr,
//   - Δt is the elapsed time since reference_time in hours,
//   - V_k is equilibrium_argument_deg: the Greenwich equilibrium argument
//     evaluated once, server-side, at the absolute instant reference_time.
//     Concretely it is domain.NodalCorrection.GetEquilibriumArgument(k, T),
//     where T is the absolute time reference_time expressed as hours since
//     the Unix epoch (reference_time.Sub(time.Unix(0, 0).UTC()).Hours()),
//     exactly as domain.CalculateTideHeight evaluates V(t_ref). V_k is
//     therefore a constant of the response: it does not depend on the
//     prediction time t, and it is not a relative duration,
//   - f_k(t) and u_k(t) are the nodal amplitude factor and phase correction,
//     which the client computes from the astronomical arguments at the
//     absolute prediction time t (clients that accept small errors over a
//     nodal cycle may approximate f=1, u=0).
//
// All angles are in degrees. reference_time is the FES epoch
// (2012-01-01T00:00:00Z) for the FES source and the Unix epoch for the CSV
// source, matching the server-side prediction in Execute.
func (uc *PredictionUseCase) GetParameters(req ParametersRequest) (*ParametersResponse, error) {
	// Validate request.
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrValidation, err)
	}

	// Share the parameter resolution (constituent loading, bathymetry
	// metadata, overrides, datum handling, reference epoch) with Execute.
	resolved, err := uc.resolvePredictionParams(PredictionRequest{
		Lat:       req.Lat,
		Lon:       req.Lon,
		StationID: req.StationID,
		Source:    req.Source,
	})
	if err != nil {
		return nil, err
	}

	// Evaluate the Greenwich equilibrium argument V at the absolute reference
	// epoch (hours since Unix epoch), exactly as CalculateTideHeight does.
	nodal := domain.NewAstronomicalNodalCorrection()
	refAbsHours := resolved.refTime.Sub(time.Unix(0, 0).UTC()).Hours()

	constituents := make([]ConstituentParameter, len(resolved.constituents))
	for i, c := range resolved.constituents {
		constituents[i] = ConstituentParameter{
			Name:                   c.Name,
			SpeedDegPerHr:          c.SpeedDegPerHr,
			AmplitudeM:             c.AmplitudeM,
			PhaseDeg:               c.PhaseDeg,
			EquilibriumArgumentDeg: nodal.GetEquilibriumArgument(c.Name, refAbsHours),
		}
	}

	response := &ParametersResponse{
		Source:        resolved.source,
		Datum:         datumMSL,
		MSL:           resolved.msl,
		ReferenceTime: resolved.refTime.Format(time.RFC3339),
		Constituents:  constituents,
		Meta: map[string]string{
			"model": "harmonic_v0",
		},
	}

	// Echo the query identity: lat/lon or station_id, matching Validate's
	// treatment of a pointer to an empty StationID as absent.
	if req.StationID != nil && *req.StationID != "" {
		response.StationID = req.StationID
	} else {
		response.Location = &LocationPoint{Lat: *req.Lat, Lon: *req.Lon}
	}

	// Add metadata if available (same fields as predictions).
	if resolved.metadata != nil {
		if resolved.metadata.DepthM != nil {
			response.SeabedDepth = resolved.metadata.DepthM
		}
		if resolved.metadata.DatumName != "" {
			response.Meta["datum_name"] = resolved.metadata.DatumName
		}
		if resolved.metadata.SourceName != "" {
			response.Meta["metadata_source"] = resolved.metadata.SourceName
		}
	}

	// Add attribution based on source (same as predictions).
	if resolved.source == sourceCSV {
		response.Meta["attribution"] = "Mock CSV (for dev). Replace with FES later."
	} else {
		response.Meta["attribution"] = "FES2014/2022 tidal model"
	}

	return response, nil
}
