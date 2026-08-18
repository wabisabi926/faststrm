# build-fnos.ps1 - fnOS .fpk package builder (Windows)
#
# Usage:
#   .\scripts\build-fnos.ps1 [-Arch amd64|arm64|all] [-SkipBuild]
#
# Dependencies:
#   - go (cross-compile Linux binary)
#   - templ (optional, generate template code)
#   - fnpack (fnOS official packaging tool, Linux-only binary)
#       Download: https://static2.fnnas.com/fnpack/fnpack-1.2.1-linux-amd64
#       On Windows: run via WSL, or build on Linux/CI
#
# Output:
#   dist/faststrm-{arch}-{version}.fpk
#
[CmdletBinding()]
param(
    [ValidateSet('amd64','arm64','all')]
    [string]$Arch = 'amd64',
    [switch]$SkipBuild
)

$ErrorActionPreference = 'Stop'
$Root = Split-Path -Parent $PSScriptRoot
Set-Location $Root

# Read version from amd64 manifest
$Man = Get-Content (Join-Path $Root "deploy\fnos\faststrm-amd64\manifest") -Raw
$Version = 'dev'
if ($Man -match '(?m)^\s*version\s*=\s*([^\r\n]+)') {
    $Version = $Matches[1].Trim()
}

$Dist = Join-Path $Root 'dist'
New-Item -ItemType Directory -Force -Path $Dist | Out-Null

$ArchList = if ($Arch -eq 'all') { @('amd64','arm64') } else { @($Arch) }

# ---- Check fnpack ----
$FnpackCmd = $null
if (Get-Command fnpack -ErrorAction SilentlyContinue) {
    $FnpackCmd = 'fnpack'
} elseif (Get-Command wsl -ErrorAction SilentlyContinue) {
    # Check if fnpack is available in WSL
    $wslCheck = & wsl which fnpack 2>$null
    if ($wslCheck) { $FnpackCmd = 'wsl' }
}
if (-not $FnpackCmd) {
    Write-Host "ERROR: fnpack not found." -ForegroundColor Red
    Write-Host "  fnpack is a Linux-only binary from fnOS." -ForegroundColor Yellow
    Write-Host "  Options:" -ForegroundColor Yellow
    Write-Host "    1. Install in WSL:  curl -fsSL https://static2.fnnas.com/fnpack/fnpack-1.2.1-linux-amd64 -o /tmp/fnpack && chmod +x /tmp/fnpack && sudo mv /tmp/fnpack /usr/local/bin/" -ForegroundColor Yellow
    Write-Host "    2. Run on Linux/CI: ./scripts/build-fnos.sh" -ForegroundColor Yellow
    exit 1
}

# ---- templ generate (once) ----
if (-not $SkipBuild) {
    if (Get-Command templ -ErrorAction SilentlyContinue) {
        Write-Host "==> templ generate" -ForegroundColor Cyan
        & templ generate
        if ($LASTEXITCODE -ne 0) { throw "templ generate failed" }
    } else {
        Write-Warning "templ not found. Skipping templ generate."
    }
    if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
        throw "go not found in PATH"
    }
}

# ---- Save & set env for cross-compile ----
$SavedCGO = $env:CGO_ENABLED
$SavedGOOS = $env:GOOS
$SavedGOARCH = $env:GOARCH

try {
    foreach ($A in $ArchList) {
        $Stage   = Join-Path $Root "deploy\fnos\faststrm-$A"
        $AppDir  = Join-Path $Stage 'app'
        $Manifest= Join-Path $Stage 'manifest'

        # Verify manifest arch/platform
        Write-Host "==> [$A] verify manifest" -ForegroundColor Cyan
        $manContent = Get-Content $Manifest
        if ($A -eq 'amd64') {
            $archLine = $manContent | Where-Object { $_ -match '^\s*arch\s*=' }
            if ($archLine -notmatch 'x86_64') { throw "manifest arch mismatch for amd64: $archLine" }
        } else {
            $platLine = $manContent | Where-Object { $_ -match '^\s*platform\s*=' }
            if ($platLine -notmatch 'arm') { throw "manifest platform mismatch for arm64: $platLine" }
        }

        if (-not $SkipBuild) {
            Write-Host "==> [$A] building Go binary (linux/$A)" -ForegroundColor Cyan
            $BinOut = Join-Path $AppDir 'faststrm'
            $env:CGO_ENABLED = '0'
            $env:GOOS = 'linux'
            $env:GOARCH = $A
            $LdFlags = "-s -w -X main.version=$Version"
            & go build -trimpath -ldflags $LdFlags -o $BinOut (Join-Path $Root "cmd\server")
            if ($LASTEXITCODE -ne 0) { throw "go build failed (arch=$A)" }
            $MB = [Math]::Round((Get-Item $BinOut).Length / 1MB, 2)
            Write-Host ("    binary -> {0} ({1} MB)" -f $BinOut, $MB)
        }

        # fnpack build
        Write-Host "==> [$A] fnpack build" -ForegroundColor Cyan

        # Convert Windows path to WSL path if using WSL
        if ($FnpackCmd -eq 'wsl') {
            # Convert stage path to WSL path
            $wslRoot = (& wsl wslpath -a $Stage).Trim()
            & wsl bash -lc "cd '$wslRoot' && fnpack build"
        } else {
            $stageDir = $Stage
            Push-Location $stageDir
            & fnpack build
            Pop-Location
        }
        if ($LASTEXITCODE -ne 0) { throw "fnpack build failed (arch=$A)" }

        # Check for generated .fpk
        $FpkPath = Join-Path $Stage 'faststrm.fpk'
        if (-not (Test-Path $FpkPath)) {
            throw "fnpack did not generate faststrm.fpk in $Stage"
        }

        # Copy to dist/
        $PkgName = "faststrm-$A-$Version.fpk"
        $PkgPath = Join-Path $Dist $PkgName
        Copy-Item $FpkPath $PkgPath -Force
        Remove-Item $FpkPath -Force

        $Size = [Math]::Round((Get-Item $PkgPath).Length / 1MB, 2)
        Write-Host ("    produced {0} ({1} MB)" -f $PkgPath, $Size) -ForegroundColor Green
    }
} finally {
    # Restore env vars
    $env:CGO_ENABLED = $SavedCGO
    $env:GOOS = $SavedGOOS
    $env:GOARCH = $SavedGOARCH
}

Write-Host ""
Write-Host "Done. Artifacts in: $Dist" -ForegroundColor Green
