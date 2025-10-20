# FES2014 Data Setup Guide

Complete guide for downloading and using FES2014 tidal model data with the Tide API.

## Overview

FES (Finite Element Solution) 2014 is a high-resolution global tidal model developed by LEGOS, CNES, and CLS. The Tide API can use FES NetCDF data to provide accurate tidal predictions for any location worldwide.

## Prerequisites

### 1. AVISO+ Account

FES2014 data requires registration with AVISO+:

1. Visit: https://www.aviso.altimetry.fr/
2. Click "Register" and create an account
3. Accept the data access terms
4. Wait for account activation (usually 24-48 hours)

### 2. Required Software

#### Option A: Using lftp (Recommended)

```bash
# macOS
brew install lftp

# Ubuntu/Debian
sudo apt-get install lftp

# CentOS/RHEL
sudo yum install lftp
```

#### Option B: Using curl

curl supports SFTP and is usually pre-installed on macOS/Linux.

---

## Quick Start (3 Steps)

### Step 1: Setup Credentials

```bash
cd /Users/ngs/src/tides-api
make fes-setup
```

This will prompt for your AVISO username and password, then save them securely to `.fes_credentials` (git-ignored).

### Step 2: Download FES Data

**Option A: Download Major Constituents Only (Recommended for testing)**

```bash
make fes-download-major
```

Downloads: M2, S2, K1, O1, N2, K2, P1, Q1 (~500MB, 5-10 minutes)

**Option B: Download All Constituents (Complete dataset)**

```bash
make fes-download-all
```

Downloads: All 34 constituents (~5GB, 30+ minutes)

### Step 3: Verify Installation

```bash
make fes-check
```

Expected output:
```
NetCDF files found:
  - m2_amplitude.nc (45M)
  - m2_phase.nc (45M)
  - s2_amplitude.nc (45M)
  - s2_phase.nc (45M)
  ...

Constituents detected:
  - m2
  - s2
  - k1
  - o1
```

---

## Makefile Commands Reference

### Setup and Configuration

```bash
make fes-setup              # Interactive credential setup
```

### Downloading Data

```bash
# List files on AVISO server
make fes-list

# Download single constituent
make fes-download-constituent CONST=m2

# Download major constituents (M2, S2, K1, O1, N2, K2, P1, Q1)
make fes-download-major

# Download ALL constituents (~5GB)
make fes-download-all

# Download specific file using curl
make fes-download-curl FILE=m2_amplitude.nc
```

### Managing Data

```bash
# Check downloaded files
make fes-check

# Remove all FES NetCDF files
make fes-clean

# Generate mock NetCDF files for testing (Python required)
make fes-mock
```

---

## Manual Download (Alternative)

If you prefer manual download or the Makefile doesn't work:

### Using lftp

```bash
# Connect to AVISO server
lftp -u YOUR_USERNAME,YOUR_PASSWORD sftp://ftp-access.aviso.altimetry.fr:2221

# Navigate to FES directory
cd /auxiliary/tide_model/fes2014

# List files
ls

# Download specific constituent
mget m2*.nc -o ./data/fes/

# Download all files
mirror --continue --verbose --only-newer . ./data/fes/

# Exit
bye
```

### Using curl

```bash
# Download single file
curl -u YOUR_USERNAME:YOUR_PASSWORD \
  "sftp://ftp-access.aviso.altimetry.fr:2221/auxiliary/tide_model/fes2014/m2_amplitude.nc" \
  -o "./data/fes/m2_amplitude.nc"
```

### Using GUI SFTP Client

1. **FileZilla / Cyberduck / WinSCP**
   - Protocol: SFTP
   - Host: ftp-access.aviso.altimetry.fr
   - Port: 2221
   - Username: Your AVISO username
   - Password: Your AVISO password

2. Navigate to: `/auxiliary/tide_model/fes2014/`

3. Download files to: `./data/fes/`

---

## FES File Structure

### Expected File Naming

The API supports multiple naming conventions:

```
{constituent}_amplitude.nc    # Preferred
{constituent}_phase.nc        # Preferred

{constituent}_amp.nc          # Also supported
{constituent}_pha.nc          # Also supported

{constituent}.nc              # Combined file (if both variables present)
```

### Example Structure

```
data/fes/
├── m2_amplitude.nc
├── m2_phase.nc
├── s2_amplitude.nc
├── s2_phase.nc
├── k1_amplitude.nc
├── k1_phase.nc
├── o1_amplitude.nc
├── o1_phase.nc
├── n2_amplitude.nc
├── n2_phase.nc
└── ...
```

### NetCDF File Format

Each file should contain:

**Amplitude File:**
- Dimensions: `lat`, `lon`
- Variables:
  - `lat` (or `latitude`): Latitude array [-90, 90]
  - `lon` (or `longitude`): Longitude array [-180, 180] or [0, 360]
  - `amplitude` (or `amp`): 2D grid of amplitude values (meters)

**Phase File:**
- Dimensions: `lat`, `lon`
- Variables:
  - `lat` (or `latitude`): Latitude array
  - `lon` (or `longitude`): Longitude array
  - `phase` (or `pha`): 2D grid of phase values (degrees)

---

## Available Constituents

### FES2014 Standard Constituents (34 total)

#### Major Semidiurnal (12-hour period)
- **M2** - Principal lunar semidiurnal (largest constituent)
- **S2** - Principal solar semidiurnal
- **N2** - Larger lunar elliptic semidiurnal
- **K2** - Lunisolar semidiurnal

#### Major Diurnal (24-hour period)
- **K1** - Lunar diurnal
- **O1** - Lunar diurnal
- **P1** - Solar diurnal
- **Q1** - Solar diurnal

#### Shallow Water Components
- M4, MS4, MN4, 2N2, MU2, NU2, L2, T2
- M6, M8, MKS2, 2SM2

#### Long Period
- Mf, Mm, Ssa, Sa, Msqm, Mtm

#### Others
- MSf, Lambda2, 2Q1, Sigma1, Rho1, Chi1, Pi1, Phi1
- Theta1, J1, OO1, Eps2, La2, S1

---

## Testing the Integration

### 1. Generate Mock Data (Quick Test)

If you don't have AVISO credentials yet:

```bash
# Install Python dependencies
pip install numpy netCDF4

# Generate mock files
make fes-mock

# This creates synthetic data for the Japan/Asia-Pacific region
```

### 2. Test with Real FES Data

```bash
# Start the API server
make run

# Test with Tokyo coordinates (35.6762°N, 139.6503°E)
curl 'http://localhost:8080/v1/tides/predictions?lat=35.6762&lon=139.6503&start=2025-10-21T00:00:00Z&end=2025-10-21T12:00:00Z&interval=10m' | jq .

# Expected response includes:
# "source": "fes"
# "constituents": ["M2", "S2", "K1", "O1", ...]
# "meta": {"attribution": "FES2014/2022 tidal model"}
```

### 3. Compare with CSV Mock Data

```bash
# CSV-based (station)
curl 'http://localhost:8080/v1/tides/predictions?station_id=tokyo&...' | jq '.extrema'

# FES-based (lat/lon)
curl 'http://localhost:8080/v1/tides/predictions?lat=35.6762&lon=139.6503&...' | jq '.extrema'
```

---

## Troubleshooting

### Connection Issues

**Error: "Connection refused"**

```bash
# Test SFTP connection
sftp -P 2221 YOUR_USERNAME@ftp-access.aviso.altimetry.fr

# If this fails, check:
# 1. Your AVISO account is activated
# 2. Firewall allows port 2221
# 3. Username/password are correct
```

**Error: "Authentication failed"**

```bash
# Re-setup credentials
rm .fes_credentials
make fes-setup

# Or manually edit .fes_credentials:
echo "your_username" > .fes_credentials
echo "your_password" >> .fes_credentials
chmod 600 .fes_credentials
```

### Data Issues

**Error: "No FES NetCDF files found"**

```bash
# Check if files exist
ls -lh data/fes/*.nc

# If empty, download data
make fes-download-major
```

**Error: "Failed to interpolate"**

```bash
# Verify file integrity
ncdump -h data/fes/m2_amplitude.nc

# Check if coordinates cover your location
# FES2014 is global, but mock files only cover 20-50°N, 120-150°E
```

**Error: "Latitude/longitude variable not found"**

The API tries multiple variable names:
- `lat`, `latitude`, `y`
- `lon`, `longitude`, `x`

If your files use different names, the NetCDF loader may fail. Check with:

```bash
ncdump -h data/fes/m2_amplitude.nc | grep dimensions
```

### Performance Issues

**Slow first request**

- First request loads all constituent grids into memory cache
- Subsequent requests use cached grids
- Loading 8 constituents takes ~5-10 seconds

**Large memory usage**

- Each constituent grid: ~45MB × 2 (amp + phase) = 90MB
- 8 constituents: ~720MB
- 34 constituents: ~3GB

Consider:
- Only download constituents you need
- Increase server memory for production

---

## Security Notes

### Credential Storage

- `.fes_credentials` is automatically added to `.gitignore`
- File permissions are set to `600` (owner read/write only)
- Never commit this file to version control

### Alternative: Environment Variables

Instead of `.fes_credentials`, you can use environment variables:

```bash
export FES_USER="your_username"
export FES_PASS="your_password"
make fes-download-major
```

---

## Data License and Attribution

### FES2014 License

FES2014 data is provided by AVISO+ under specific terms:

- **Academic/Research**: Free with registration
- **Commercial**: May require license agreement
- **Redistribution**: Not permitted without authorization

### Required Attribution

When using FES data in publications or applications:

> Carrère L., Lyard F., Cancet M., Guillot A. (2016). FES 2014, a new tidal model—Validation results and perspectives for improvements. In Proceedings of the ESA living planet symposium (pp. 9-13).

The API automatically includes this in responses when using FES data:

```json
{
  "meta": {
    "attribution": "FES2014/2022 tidal model"
  }
}
```

---

## Advanced Configuration

### Custom FES Directory

```bash
# Use different directory
make fes-download-major FES_DIR=/path/to/custom/fes

# Set in environment
export FES_DIR=/path/to/custom/fes
make run
```

### Download Resume

All download commands support resume (`-c` flag for lftp, automatic for curl):

```bash
# If download is interrupted, simply re-run
make fes-download-major

# lftp will resume from where it left off
```

### Parallel Downloads

```bash
# Download multiple constituents in parallel (faster)
for const in m2 s2 k1 o1; do
  make fes-download-constituent CONST=$const &
done
wait
```

---

## Next Steps

1. ✅ **Setup**: `make fes-setup`
2. ✅ **Download**: `make fes-download-major`
3. ✅ **Verify**: `make fes-check`
4. ✅ **Test**: `make run` then curl with lat/lon
5. 📚 **Read**: [README.md](README.md) for API documentation

## Support

- **AVISO+ Help**: https://www.aviso.altimetry.fr/en/data/data-access.html
- **FES Documentation**: https://www.aviso.altimetry.fr/en/data/products/auxiliary-products/global-tide-fes.html
- **API Issues**: GitHub Issues (if applicable)

---

**Last Updated**: 2025-10-21
