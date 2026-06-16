#!/bin/bash
set -e

echo "Building backend binaries for all platforms..."

# 清理旧文件
rm -rf packages/elysia-api/assets/bin
mkdir -p packages/elysia-api/assets/bin

# 构建 WebUI 并同步到 backend 内嵌目录（//go:embed all:dist）。
# 这样四个平台的二进制都会嵌入最新前端产物，开箱即用、零配置。
echo "Building WebUI..."
( cd packages/webui && yarn build )
rm -rf backend/webui/dist
mkdir -p backend/webui/dist
cp -r packages/webui/dist/* backend/webui/dist/
echo "WebUI assets synced to backend/webui/dist"

# 编译各平台版本，直接产出到 elysia-api 插件的 assets/bin
cd backend

echo "Building Windows amd64..."
GOOS=windows GOARCH=amd64 go build -o ../packages/elysia-api/assets/bin/elysia-backend.exe .

echo "Building Linux amd64..."
GOOS=linux GOARCH=amd64 go build -o ../packages/elysia-api/assets/bin/elysia-backend-linux .

echo "Building macOS amd64 (Intel)..."
GOOS=darwin GOARCH=amd64 go build -o ../packages/elysia-api/assets/bin/elysia-backend-darwin-amd64 .

echo "Building macOS arm64 (Apple Silicon)..."
GOOS=darwin GOARCH=arm64 go build -o ../packages/elysia-api/assets/bin/elysia-backend-darwin-arm64 .

cd ..

echo ""
echo "Done! Binaries built into packages/elysia-api/assets/bin/"
ls -lh packages/elysia-api/assets/bin/
