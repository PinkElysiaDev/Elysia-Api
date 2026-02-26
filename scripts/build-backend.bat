@echo off
echo Building backend binaries for all platforms...

rmdir /s /q packages\orchestrator\assets\bin 2>nul
mkdir packages\orchestrator\assets\bin

cd backend

echo Building Windows amd64...
set GOOS=windows
set GOARCH=amd64
go build -o ..\packages\orchestrator\assets\bin\elysia-backend.exe .

echo Building Linux amd64...
set GOOS=linux
set GOARCH=amd64
go build -o ..\packages\orchestrator\assets\bin\elysia-backend-linux .

echo Building macOS amd64 (Intel)...
set GOOS=darwin
set GOARCH=amd64
go build -o ..\packages\orchestrator\assets\bin\elysia-backend-darwin-amd64 .

echo Building macOS arm64 (Apple Silicon)...
set GOOS=darwin
set GOARCH=arm64
go build -o ..\packages\orchestrator\assets\bin\elysia-backend-darwin-arm64 .

cd ..

echo.
echo Done! Binaries copied to packages\orchestrator\assets\bin\
dir packages\orchestrator\assets\bin\
