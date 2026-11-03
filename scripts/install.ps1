# BeeChief install & update script for Windows PowerShell.
param(
    [string]$Version = "latest",
    [string]$InstallDir = "$env:LOCALAPPDATA\Programs\BeeChief",
    [switch]$FromSource
)

$ErrorActionPreference = "Stop"
$Repo = if ($env:BEECHIEF_REPO) { $env:BEECHIEF_REPO } else { "meistro57/beechief" }

New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
$BinPath = Join-Path $InstallDir "beechief.exe"

if ($FromSource -or (Test-Path "go.mod")) {
    Write-Host "==> Building BeeChief from source..."
    $env:CGO_ENABLED = "0"
    go build -trimpath -o $BinPath ./cmd/beechief
} else {
    $Arch = if ([Environment]::Is64BitOperatingSystem) { "amd64" } else { throw "32-bit Windows is not supported" }
    if ($Version -eq "latest") {
        $Release = Invoke-RestMethod -Uri "https://api.github.com/repos/$Repo/releases/latest"
        $Version = $Release.tag_name
    }
    $Archive = "beechief-$Version-windows-$Arch.zip"
    $Url = "https://github.com/$Repo/releases/download/$Version/$Archive"
    $TmpZip = Join-Path $env:TEMP $Archive
    $TmpExtract = Join-Path $env:TEMP "beechief-extract-$PID"

    Write-Host "==> Downloading $Url..."
    Invoke-WebRequest -Uri $Url -OutFile $TmpZip
    Expand-Archive -Path $TmpZip -DestinationPath $TmpExtract -Force
    Copy-Item (Join-Path $TmpExtract "beechief-$Version-windows-$Arch\beechief.exe") -Destination $BinPath -Force
    Remove-Item $TmpZip -Force -ErrorAction SilentlyContinue
    Remove-Item $TmpExtract -Recurse -Force -ErrorAction SilentlyContinue
}

& $BinPath --version
Write-Host "==> Installed BeeChief to $BinPath"
