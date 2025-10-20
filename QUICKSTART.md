# Quick Start Guide

Get the Tide API running in under 5 minutes!

## 1. Start the Server

```bash
# Make sure you're in the project directory
cd tides-api

# Run the server
make run
```

You should see:
```
Starting Tide API server...
Server listening on :8080
```

## 2. Test the API

Open a new terminal and try these commands:

### Health Check
```bash
curl http://localhost:8080/healthz
```

Expected response:
```json
{
  "status": "ok",
  "time": "2025-10-21T12:00:00Z"
}
```

### Get Tide Predictions for Tokyo

```bash
curl 'http://localhost:8080/v1/tides/predictions?station_id=tokyo&start=2025-10-21T00:00:00Z&end=2025-10-21T12:00:00Z&interval=30m'
```

This returns tide heights every 30 minutes for 12 hours, including high and low tides.

### View Available Constituents

```bash
curl http://localhost:8080/v1/constituents
```

Lists all 18 tidal constituents with their angular speeds.

## 3. Pretty Output with jq

If you have `jq` installed, you can format the JSON nicely:

```bash
# Just the extrema (high/low tides)
curl -s 'http://localhost:8080/v1/tides/predictions?station_id=tokyo&start=2025-10-21T00:00:00Z&end=2025-10-22T00:00:00Z&interval=10m' | jq '.extrema'
```

Output:
```json
{
  "highs": [
    {
      "time": "2025-10-21T11:27:29Z",
      "height_m": 0.192
    }
  ],
  "lows": [
    {
      "time": "2025-10-21T05:56:03Z",
      "height_m": -0.439
    },
    {
      "time": "2025-10-21T17:16:45Z",
      "height_m": -0.546
    }
  ]
}
```

## 4. Try Different Parameters

### Shorter Intervals (More Data Points)
```bash
curl 'http://localhost:8080/v1/tides/predictions?station_id=tokyo&start=2025-10-21T00:00:00Z&end=2025-10-21T06:00:00Z&interval=10m'
```

### Longer Time Range
```bash
curl 'http://localhost:8080/v1/tides/predictions?station_id=tokyo&start=2025-10-21T00:00:00Z&end=2025-10-23T00:00:00Z&interval=1h'
```

### Different Time Zone (Parse in Your App)
The API returns times in UTC (ISO8601 format). Your client application can convert to local time zones.

## 5. Using Make Commands

The project includes several useful `make` targets:

```bash
# Run tests
make test

# Run unit tests only
make test-unit

# Build binary
make build

# Clean build artifacts
make clean

# Format code
make fmt
```

## 6. Running with Docker

```bash
# Build Docker image
make docker-build

# Run in container
make docker-run

# Test the containerized API
curl http://localhost:8080/healthz
```

## Common Issues

### Port Already in Use

If port 8080 is already in use, set a different port:

```bash
PORT=8081 make run
```

Then use `http://localhost:8081` in your curl commands.

### Data Directory Not Found

The server looks for CSV data in `./data` by default. Make sure you're running from the project root:

```bash
cd /path/to/tides-api
make run
```

### Invalid Station ID

Currently only `tokyo` is available. To add more stations, create a CSV file:

```bash
# Create a new station file
cp data/mock_tokyo_constituents.csv data/mock_yokohama_constituents.csv

# Edit with your constituent data
# Then query with station_id=yokohama
```

## Next Steps

1. **Read the Full Documentation**: Check out [README.md](README.md) for detailed API documentation
2. **Add More Stations**: Create additional CSV files in the `data/` directory
3. **Integrate FES Data**: When ready, implement the FES NetCDF loader for real-world predictions
4. **Deploy**: Use the provided Dockerfile to deploy to your cloud platform

## Example Integration (JavaScript)

```javascript
async function getTideForToday() {
  const now = new Date();
  const tomorrow = new Date(now);
  tomorrow.setDate(tomorrow.getDate() + 1);

  const start = now.toISOString();
  const end = tomorrow.toISOString();

  const url = `http://localhost:8080/v1/tides/predictions?station_id=tokyo&start=${start}&end=${end}&interval=10m`;

  const response = await fetch(url);
  const data = await response.json();

  console.log('High tides today:', data.extrema.highs);
  console.log('Low tides today:', data.extrema.lows);

  return data;
}
```

## Need Help?

- Check the [README.md](README.md) for full documentation
- Review [data/README_DATA.md](data/README_DATA.md) for data format details
- Look at test files in `internal/*/` for usage examples

Happy tide predicting! 🌊
