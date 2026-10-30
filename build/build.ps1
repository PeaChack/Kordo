# Builds Kordo for Windows into .\bin
#   powershell -ExecutionPolicy Bypass -File build\build.ps1 [-SkipTests]
param([switch]$SkipTests)
$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
Set-Location $root

$tags = (Get-Content "$root\build\tags.txt" -Raw).Trim()
$version = '0.1.0'
$ld = "-s -w -X github.com/kordo-vpn/kordo/windows/service.Version=$version"
$winres = 'github.com/tc-hib/go-winres@v0.3.3'

if (-not $SkipTests) {
  Write-Host '== tests' -ForegroundColor Cyan
  go test -tags $tags ./...
  if ($LASTEXITCODE) { throw 'tests failed' }
}

Write-Host '== resources (icon, manifest, version)' -ForegroundColor Cyan
foreach ($dir in 'windows\gui', 'cmd\kordo-svc', 'cmd\kordoctl') {
  Push-Location $dir
  go run $winres make --in winres\winres.json --out rsrc --arch amd64
  if ($LASTEXITCODE) { Pop-Location; throw "go-winres failed in $dir" }
  Pop-Location
}

New-Item -ItemType Directory -Force bin | Out-Null
Write-Host '== kordo-svc.exe' -ForegroundColor Cyan
go build -trimpath -tags $tags -ldflags $ld -o bin\kordo-svc.exe .\cmd\kordo-svc
if ($LASTEXITCODE) { throw 'build failed' }
Write-Host '== kordoctl.exe' -ForegroundColor Cyan
go build -trimpath -ldflags '-s -w' -o bin\kordoctl.exe .\cmd\kordoctl
if ($LASTEXITCODE) { throw 'build failed' }
Write-Host '== Kordo.exe (GUI)' -ForegroundColor Cyan
go build -trimpath -tags 'desktop,production' -ldflags '-s -w -H windowsgui' -o bin\Kordo.exe .\windows\gui
if ($LASTEXITCODE) { throw 'build failed' }

Copy-Item LICENSE, THIRD_PARTY_NOTICES.md bin\ -Force
Get-ChildItem bin | Format-Table Name, Length
