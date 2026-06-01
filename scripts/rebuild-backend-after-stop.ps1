param(
  [string]$RepoRoot = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path,
  [switch]$SkipPluginBuild
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$backendDir = Join-Path $RepoRoot 'backend'
$binDir = Join-Path $RepoRoot 'packages/orchestrator/assets/bin'
$stagingDir = Join-Path $backendDir '.codex-backend-build'
$goCacheDir = Join-Path $backendDir '.codex-gocache'
$goTmpDir = Join-Path $backendDir '.codex-gotmp'
$windowsBackend = Join-Path $binDir 'elysia-backend.exe'

function Write-Step([string]$Message) {
  Write-Host "==> $Message"
}

function Build-Backend([string]$Goos, [string]$Goarch, [string]$OutputName) {
  $env:GOOS = $Goos
  $env:GOARCH = $Goarch
  $outputPath = Join-Path $stagingDir $OutputName
  Write-Step "Building $OutputName ($Goos/$Goarch)"
  go build -o $outputPath .
}

if (!(Test-Path $backendDir)) {
  throw "Backend directory not found: $backendDir"
}

New-Item -ItemType Directory -Force -Path $binDir, $stagingDir, $goCacheDir, $goTmpDir | Out-Null

$oldGoos = $env:GOOS
$oldGoarch = $env:GOARCH
$oldGoCache = $env:GOCACHE
$oldGoTmp = $env:GOTMPDIR

try {
  Write-Step 'Stopping running elysia backend process'
  $running = Get-Process -ErrorAction SilentlyContinue | Where-Object { $_.Path -eq $windowsBackend }
  if ($running) {
    $running | Stop-Process -Force
    Start-Sleep -Seconds 1
  } else {
    Write-Host 'No running elysia backend process found.'
  }

  Set-Location $backendDir
  $env:GOCACHE = (Resolve-Path $goCacheDir).Path
  $env:GOTMPDIR = (Resolve-Path $goTmpDir).Path

  Build-Backend 'windows' 'amd64' 'elysia-backend.exe'
  Build-Backend 'linux' 'amd64' 'elysia-backend-linux'
  Build-Backend 'darwin' 'amd64' 'elysia-backend-darwin-amd64'
  Build-Backend 'darwin' 'arm64' 'elysia-backend-darwin-arm64'

  Write-Step 'Moving binaries into orchestrator assets'
  Copy-Item -LiteralPath (Join-Path $stagingDir 'elysia-backend.exe') -Destination (Join-Path $binDir 'elysia-backend.exe') -Force
  Copy-Item -LiteralPath (Join-Path $stagingDir 'elysia-backend-linux') -Destination (Join-Path $binDir 'elysia-backend-linux') -Force
  Copy-Item -LiteralPath (Join-Path $stagingDir 'elysia-backend-darwin-amd64') -Destination (Join-Path $binDir 'elysia-backend-darwin-amd64') -Force
  Copy-Item -LiteralPath (Join-Path $stagingDir 'elysia-backend-darwin-arm64') -Destination (Join-Path $binDir 'elysia-backend-darwin-arm64') -Force

  Write-Step 'Backend binaries rebuilt'
  Get-ChildItem -LiteralPath $binDir -File | Where-Object { $_.Name -like 'elysia-backend*' } | Select-Object Name, Length, LastWriteTime | Format-Table -AutoSize

  if (!$SkipPluginBuild) {
    Write-Step 'Building Koishi plugin packages'
    Set-Location $RepoRoot
    yarn build
  }
} finally {
  $env:GOOS = $oldGoos
  $env:GOARCH = $oldGoarch
  $env:GOCACHE = $oldGoCache
  $env:GOTMPDIR = $oldGoTmp
}
