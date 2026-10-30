# Builds Portico for Windows into .\bin
#   powershell -ExecutionPolicy Bypass -File build\build.ps1 [-SkipTests]
param([switch]$SkipTests)
$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
Set-Location $root

$tags = (Get-Content "$root\build\tags.txt" -Raw).Trim()
$version = '0.1.0'
$ld = "-s -w -X github.com/portico-vpn/portico/windows/service.Version=$version"
$winres = 'github.com/tc-hib/go-winres@v0.3.3'

if (-not $SkipTests) {
  Write-Host '== tests' -ForegroundColor Cyan
  go test -tags $tags ./...
  if ($LASTEXITCODE) { throw 'tests failed' }
}

Write-Host '== resources (icon, manifest, version)' -ForegroundColor Cyan
foreach ($dir in 'windows\gui', 'cmd\portico-svc', 'cmd\porticoctl') {
  Push-Location $dir
  go run $winres make --in winres\winres.json --out rsrc --arch amd64
  if ($LASTEXITCODE) { Pop-Location; throw "go-winres failed in $dir" }
  Pop-Location
}

New-Item -ItemType Directory -Force bin | Out-Null
Write-Host '== portico-svc.exe' -ForegroundColor Cyan
go build -trimpath -tags $tags -ldflags $ld -o bin\portico-svc.exe .\cmd\portico-svc
if ($LASTEXITCODE) { throw 'build failed' }
Write-Host '== porticoctl.exe' -ForegroundColor Cyan
go build -trimpath -ldflags '-s -w' -o bin\porticoctl.exe .\cmd\porticoctl
if ($LASTEXITCODE) { throw 'build failed' }
Write-Host '== Portico.exe (GUI)' -ForegroundColor Cyan
go build -trimpath -tags 'desktop,production' -ldflags '-s -w -H windowsgui' -o bin\Portico.exe .\windows\gui
if ($LASTEXITCODE) { throw 'build failed' }

Copy-Item LICENSE, THIRD_PARTY_NOTICES.md bin\ -Force
Get-ChildItem bin | Format-Table Name, Length
