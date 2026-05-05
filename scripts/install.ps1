# Hush installer for Windows (PowerShell).
#
# Usage:
#   iwr https://github.com/shuakami/hush/releases/latest/download/install.ps1 -useb | iex
#
# Env overrides:
#   $env:HUSH_VERSION = "v0.1.0"
#   $env:HUSH_PREFIX  = "C:\\tools\\hush"

$ErrorActionPreference = "Stop"

$Repo    = "shuakami/hush"
$Version = if ($env:HUSH_VERSION) { $env:HUSH_VERSION } else { "latest" }
$Prefix  = if ($env:HUSH_PREFIX)  { $env:HUSH_PREFIX  } else { "$env:LOCALAPPDATA\hush" }

$arch = if ([System.Environment]::Is64BitOperatingSystem) { "amd64" } else { "386" }
if ($arch -ne "amd64") {
    Write-Error "unsupported arch: $arch (only amd64 release is currently published)"
}

if ($Version -eq "latest") {
    $url = "https://github.com/$Repo/releases/latest/download/hush_windows_amd64.zip"
} else {
    $url = "https://github.com/$Repo/releases/download/$Version/hush_windows_amd64.zip"
}

New-Item -ItemType Directory -Force -Path $Prefix | Out-Null
$tmp = Join-Path $env:TEMP ("hush-install-" + [guid]::NewGuid().ToString())
New-Item -ItemType Directory -Force -Path $tmp | Out-Null

try {
    Write-Host "==> downloading $url"
    $zip = Join-Path $tmp "hush.zip"
    Invoke-WebRequest -Uri $url -OutFile $zip -UseBasicParsing

    Write-Host "==> extracting"
    Expand-Archive -Path $zip -DestinationPath $tmp -Force

    $src = Join-Path $tmp "hush.exe"
    $dst = Join-Path $Prefix "hush.exe"
    Write-Host "==> installing to $dst"
    Copy-Item -Force $src $dst

    $userPath = [Environment]::GetEnvironmentVariable("Path", "User")
    if ($userPath -notlike "*$Prefix*") {
        Write-Host "==> adding $Prefix to user PATH"
        [Environment]::SetEnvironmentVariable("Path", "$userPath;$Prefix", "User")
        $env:Path = "$env:Path;$Prefix"
    }

    Write-Host ""
    & $dst --version
    Write-Host ""
    Write-Host "installed: $dst"
    Write-Host "next:"
    Write-Host "  hush bootstrap   # initialize a local server"
    Write-Host "  hush --help"
} finally {
    Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
}
