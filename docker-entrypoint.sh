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
FES_FILES=0
if [ -d "$FES_DIR" ]; then
    FES_FILES=$(find "$FES_DIR" -name '*.nc' 2>/dev/null | wc -l | tr -d '[:space:]')
fi
if [ "$FES_FILES" -gt 0 ]; then
    echo "FES Data: Found $FES_FILES NetCDF files"
else
    echo "FES Data: Not found (will use CSV mock data only)"
fi

# Check if CSV data exists
CSV_FILES=0
if [ -d "$DATA_DIR" ]; then
    CSV_FILES=$(find "$DATA_DIR" -name '*.csv' 2>/dev/null | wc -l | tr -d '[:space:]')
fi
if [ "$CSV_FILES" -gt 0 ]; then
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
