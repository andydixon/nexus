# build.ps1 — Windows build script for Alchementary Link
# Usage: .\build.ps1 [dev|build|test|clean|deps]
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

function Write-Info($Message) { Write-Host "[link] $Message" -ForegroundColor Green }
function Write-Warn($Message) { Write-Host "[link] $Message" -ForegroundColor Yellow }
function Write-Err($Message)  { Write-Host "[link] $Message" -ForegroundColor Red }

function Test-BoolEnabled([string]$Value) {
    return $Value -match '^(1|true|TRUE|yes|YES|on|ON)$'
}

function Get-WailsPath {
    $wails = Get-Command wails -ErrorAction SilentlyContinue
    if ($wails) { return $wails.Source }

    $goPath = & go env GOPATH
    $fallback = Join-Path $goPath 'bin\wails.exe'
    if (Test-Path $fallback) { return $fallback }
    return $null
}

function Check-Deps {
    if (-not (Get-Command go -ErrorAction SilentlyContinue)) { throw 'Go is not installed.' }
    if (-not (Get-Command node -ErrorAction SilentlyContinue)) { throw 'Node.js is not installed.' }
    if (-not (Get-Command npm -ErrorAction SilentlyContinue)) { throw 'npm is not installed.' }

    $wailsPath = Get-WailsPath
    if (-not $wailsPath) {
        throw 'Wails is not installed. Run: go install github.com/wailsapp/wails/v2/cmd/wails@latest'
    }

    Write-Info "Go:    $(& go version)"
    Write-Info "Node:  $(& node --version)"
    Write-Info "Wails: $(& $wailsPath version | Select-Object -First 1)"

    return $wailsPath
}

function Install-Deps {
    Write-Info 'Installing Go dependencies...'
    & go mod download

    Write-Info 'Installing frontend dependencies...'
    Push-Location 'frontend'
    & npm install
    Pop-Location
}

function Get-BuildVersion {
    try {
        $tag = & git describe --tags --abbrev=0 2>$null
        if (-not $tag) { $tag = 'dev' }
    } catch {
        $tag = 'dev'
    }

    try {
        $commit = & git rev-parse --short HEAD 2>$null
        if (-not $commit) { $commit = 'unknown' }
    } catch {
        $commit = 'unknown'
    }

    return "$tag-$commit"
}

function Get-Ldflags([string]$Version) {
    $flags = "-s -w -buildid= -X github.com/andydixon/link/internal/api.appVersion=$Version"
    if ($env:LINK_UPDATE_URL) {
        $flags += " -X github.com/andydixon/link/internal/api.appUpdateURL=$($env:LINK_UPDATE_URL)"
    }
    return $flags
}

function Dev {
    $wailsPath = Check-Deps
    Install-Deps
    Write-Info 'Starting development mode...'
    & $wailsPath dev
}

function Build {
    $wailsPath = Check-Deps
    Install-Deps
    $version = Get-BuildVersion
    $ldflags = Get-Ldflags $version

    $env:CGO_ENABLED = '0'
    Remove-Item Env:CGO_CFLAGS -ErrorAction SilentlyContinue
    Remove-Item Env:CGO_CXXFLAGS -ErrorAction SilentlyContinue

    $buildArgs = @('build', '-trimpath')
    if (Test-BoolEnabled $env:LINK_COMPRESS) {
        if (-not (Get-Command upx -ErrorAction SilentlyContinue)) {
            throw 'LINK_COMPRESS=1 requires UPX. Install it first.'
        }
        $buildArgs += '-upx'
        if ($env:LINK_UPX_FLAGS) {
            $buildArgs += @('-upxflags', $env:LINK_UPX_FLAGS)
        }
    }
    $buildArgs += @('-ldflags', $ldflags)

    Write-Info 'Building production binary (CGO_ENABLED=0)...'
    & $wailsPath @buildArgs
    Write-Info 'Build complete: build\bin\'
}

function Run-Tests {
    Write-Info 'Running tests...'
    & go test ./... -v -count=1
}

function Clean {
    Write-Info 'Cleaning build artefacts...'
    Remove-Item -Recurse -Force -ErrorAction SilentlyContinue build\bin
    Remove-Item -Recurse -Force -ErrorAction SilentlyContinue frontend\node_modules
    Remove-Item -Recurse -Force -ErrorAction SilentlyContinue frontend\src\js\lib
    Remove-Item -Recurse -Force -ErrorAction SilentlyContinue frontend\src\css\lib
    & go clean -testcache
    Write-Info 'Clean complete'
}

$task = $args[0]
if (-not $task) { $task = 'build' }

switch ($task) {
    'dev'   { Dev }
    'build' { Build }
    'test'  { Run-Tests }
    'clean' { Clean }
    'deps'  { Check-Deps | Out-Null; Install-Deps }
    default {
        Write-Err 'Usage: .\\build.ps1 [dev|build|test|clean|deps]'
        exit 1
    }
}
