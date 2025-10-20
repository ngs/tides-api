# Tidal Constituent Data

This directory contains tidal constituent data used by the API.

## Mock CSV Data (for Development)

Mock CSV files are used for testing and development without requiring FES NetCDF files.

### File Naming Convention

- Format: `mock_{station_id}_constituents.csv`
- Example: `mock_tokyo_constituents.csv` for station_id="tokyo"

### CSV Format

Each CSV file must have the following structure:

```csv
constituent,amplitude_m,phase_deg
M2,0.62,145.0
S2,0.21,170.0
K1,0.18,30.0
O1,0.16,85.0
```

**Columns:**
- `constituent`: Name of the tidal constituent (must match standard names: M2, S2, K1, O1, etc.)
- `amplitude_m`: Amplitude in meters (positive values)
- `phase_deg`: Greenwich phase in degrees (0-360)

### Adding New Stations

To add a new mock station:

1. Create a new CSV file: `mock_{your_station_id}_constituents.csv`
2. Add constituent data for your location
3. Use the API with `station_id={your_station_id}`

Example:
```bash
curl 'http://localhost:8080/v1/tides/predictions?station_id=your_station_id&start=2025-10-21T00:00:00Z&end=2025-10-21T12:00:00Z&interval=10m'
```

## FES NetCDF Data (for Production)

### Directory Structure

Place FES2014 or FES2022 NetCDF files in a subdirectory (e.g., `data/fes/`):

```
data/fes/
  m2_amplitude.nc
  m2_phase.nc
  s2_amplitude.nc
  s2_phase.nc
  k1_amplitude.nc
  k1_phase.nc
  ...
```

### Expected NetCDF Format

Each NetCDF file should contain:
- Dimensions: `lat`, `lon`
- Variables:
  - `lat`: Latitude array (degrees North, -90 to 90)
  - `lon`: Longitude array (degrees East, -180 to 180 or 0 to 360)
  - `amplitude` or `phase`: 2D grid of values

### Configuration

Set the `FES_DIR` environment variable to point to your FES data directory:

```bash
export FES_DIR=/path/to/fes/data
```

### Using FES Data

Once FES NetCDF files are available and the loader is implemented, use the API with lat/lon:

```bash
curl 'http://localhost:8080/v1/tides/predictions?lat=35.6762&lon=139.6503&start=2025-10-21T00:00:00Z&end=2025-10-21T12:00:00Z&interval=10m'
```

## Data Attribution

### FES2014/2022

FES (Finite Element Solution) tidal model data:
- **Citation**: Carrère L., Lyard F., Cancet M., Guillot A. (2016). FES 2014, a new tidal model
- **License**: Available from AVISO+ (https://www.aviso.altimetry.fr/)
- **Note**: FES data requires registration and cannot be redistributed

When using FES data, the API automatically includes attribution in the response:

```json
{
  "meta": {
    "attribution": "FES2014/2022 tidal model"
  }
}
```

### Mock Data

Mock CSV data is for development only and should not be used for production predictions:

```json
{
  "meta": {
    "attribution": "Mock CSV (for dev). Replace with FES later."
  }
}
```

## Supported Constituents

The API supports the following tidal constituents:

### Semidiurnal (Principal period ~12 hours)
- **M2**: Principal lunar semidiurnal (28.9841042°/hr)
- **S2**: Principal solar semidiurnal (30.0000000°/hr)
- **N2**: Larger lunar elliptic semidiurnal (28.4397295°/hr)
- **K2**: Lunisolar semidiurnal (30.0821373°/hr)

### Diurnal (Principal period ~24 hours)
- **K1**: Lunar diurnal (15.0410686°/hr)
- **O1**: Lunar diurnal (13.9430356°/hr)
- **P1**: Solar diurnal (14.9589314°/hr)
- **Q1**: Solar diurnal (13.3986609°/hr)

### Shallow Water (Overtides and compound tides)
- **M4**: Shallow water overtide of M2 (57.9682084°/hr)
- **M6**: Shallow water overtide of M2 (86.9523127°/hr)
- **MK3**: Shallow water terdiurnal (44.0251729°/hr)
- **S4**: Shallow water overtide of S2 (60.0000000°/hr)
- **MN4**: Shallow water quarter diurnal (57.4238337°/hr)
- **MS4**: Shallow water quarter diurnal (58.9841042°/hr)

### Long Period
- **Mf**: Lunisolar fortnightly (1.0980331°/hr)
- **Mm**: Lunar monthly (0.5443747°/hr)
- **Ssa**: Solar semiannual (0.0821373°/hr)
- **Sa**: Solar annual (0.0410686°/hr)

## References

1. Pawlowicz, R., Beardsley, B., & Lentz, S. (2002). Classical tidal harmonic analysis including error estimates in MATLAB using T_TIDE. Computers & Geosciences, 28(8), 929-937.

2. Carrère, L., Lyard, F., Cancet, M., & Guillot, A. (2016). FES 2014, a new tidal model—Validation results and perspectives for improvements. In Proceedings of the ESA living planet symposium (pp. 9-13).

3. Foreman, M. G. G. (1977). Manual for tidal heights analysis and prediction (No. 77-10). Institute of Ocean Sciences, Patricia Bay.
