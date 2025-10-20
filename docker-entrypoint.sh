#!/bin/sh
set -e

# Docker entrypoint for Tide API
# Handles dynamic environment variable configuration

# Default values
PORT="${PORT:-8080}"
DATA_DIR="${DATA_DIR:-/app/data}"
FES_DIR="${FES_DIR:-/app/data/fes}"
TZ="${TZ:-Asia/Tokyo}"

# Export environment variables
export PORT
export DATA_DIR
export FES_DIR
export TZ

# Print configuration
echo "=================================================="
echo "Tide API - Starting Server"
echo "=================================================="
echo "Configuration:"
echo "  PORT: $PORT"
echo "  DATA_DIR: $DATA_DIR"
echo "  FES_DIR: $FES_DIR"
echo "  TZ: $TZ"
echo "=================================================="

# Check if FES data exists
if [ -d "$FES_DIR" ] && [ "$(ls -A $FES_DIR 2>/dev/null | grep -c '\.nc$' || echo 0)" -gt 0 ]; then
    FES_FILES=$(find "$FES_DIR" -name "*.nc" | wc -l)
    echo "FES Data: Found $FES_FILES NetCDF files"
else
    echo "FES Data: Not found (will use CSV mock data only)"
fi

# Check if CSV data exists
if [ -d "$DATA_DIR" ] && [ "$(ls -A $DATA_DIR 2>/dev/null | grep -c '\.csv$' || echo 0)" -gt 0 ]; then
    CSV_FILES=$(find "$DATA_DIR" -name "*.csv" | wc -l)
    echo "CSV Data: Found $CSV_FILES files"
else
    echo "CSV Data: Not found"
fi

echo "=================================================="
echo "Starting application on port $PORT..."
echo "=================================================="
echo ""

# Execute the provided command or default to running the binary
if [ "$#" -gt 0 ]; then
    # Arguments provided - execute them
    exec "$@"
else
    # No arguments - run the default binary
    exec /app/tides-api
fi
