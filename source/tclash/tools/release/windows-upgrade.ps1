param(
    [Parameter(Mandatory = $true)]
    [string] $CollectorSource
)

$ErrorActionPreference = 'Stop'
$root = Resolve-Path (Join-Path $PSScriptRoot '../..')
$arch = if ($env:PROCESSOR_ARCHITECTURE -eq 'ARM64') { 'arm64' } else { 'amd64' }
$work = Join-Path ([System.IO.Path]::GetTempPath()) ('tclash-' + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $work | Out-Null

Push-Location (Resolve-Path $CollectorSource)
try {
    $env:GOOS = 'windows'
    $env:GOARCH = $arch
    $env:CGO_ENABLED = '0'
    $collector = Join-Path $work 'tclash-collector.exe'
    & go build -mod=readonly -trimpath '-ldflags=-s -w -H windowsgui' -o $collector ./cmd/tclash-collector
    if ($LASTEXITCODE -ne 0) { throw 'Collector build failed' }
} finally {
    Remove-Item Env:GOOS, Env:GOARCH, Env:CGO_ENABLED -ErrorAction SilentlyContinue
    Pop-Location
}

Push-Location $root
try {
    $env:TCLASH_COLLECTOR_EXE = $collector
    & dart setup.dart windows --targets exe --env stable
    if ($LASTEXITCODE -ne 0) { throw 'TClash installer build failed' }
} finally {
    Remove-Item Env:TCLASH_COLLECTOR_EXE -ErrorAction SilentlyContinue
    Pop-Location
}
