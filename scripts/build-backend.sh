#!/bin/bash
set -e

echo "Building backend binaries for all platforms..."

# 清理旧文件
rm -rf packages/orchestrator/assets/bin
mkdir -p packages/orchestrator/assets/bin

# 编译各平台版本
cd backend

echo "Building Windows amd64..."
GOOS=windows GOARCH=amd64 go build -o ../packages/orchestrator/assets/bin/elysia-backend.exe .

echo "Building Linux amd64..."
GOOS=linux GOARCH=amd64 go build -o ../packages/orchestrator/assets/bin/elysia-backend-linux .

echo "Building macOS amd64 (Intel)..."
GOOS=darwin GOARCH=amd64 go build -o ../packages/orchestrator/assets/bin/elysia-backend-darwin-amd64 .

echo "Building macOS arm64 (Apple Silicon)..."
GOOS=darwin GOARCH=arm64 go build -o ../packages/orchestrator/assets/bin/elysia-backend-darwin-arm64 .

cd ..

echo ""
echo "Done! Binaries copied to packages/orchestrator/assets/bin/"
ls -lh packages/orchestrator/assets/bin/
