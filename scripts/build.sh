#!/bin/bash

set -e

echo "🚀 Building Stratix Gateway..."

cd "$(dirname "$0")"

# Download dependencies
echo "📦 Downloading dependencies..."
go mod download

# Build
echo "🔨 Building binary..."
go build -o stratix-gateway cmd/main.go

echo "✅ Build complete: ./stratix-gateway"
