#!/bin/bash

set -e

# Script to build the frontend and embed it in the Go binary

echo "🎨 Building MCPProxy Frontend..."

# Change to frontend directory
cd frontend

# Install dependencies if node_modules doesn't exist
if [ ! -d "node_modules" ]; then
    echo "📦 Installing frontend dependencies..."
    npm install
fi

# Build the frontend
echo "🔨 Building frontend for production..."
npm run build

# Verify the build
if [ ! -f "dist/index.html" ]; then
    echo "❌ Frontend build failed: dist/index.html not found"
    exit 1
fi

echo "✅ Frontend build completed successfully"
echo "📁 Frontend assets available in: frontend/dist"

# Go back to root
cd ..

# Build the Go binary with embedded frontend
echo "🔨 Building Go binary with embedded frontend..."
go build -o mcpproxy ./cmd/mcpproxy

echo "✅ Build completed successfully!"
echo "🚀 You can now run: ./mcpproxy serve"
echo "🌐 Web UI will be available at: http://localhost:8080/ui/"