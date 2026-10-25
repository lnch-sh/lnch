# Install lnch from a GitHub release of lnch-sh/lnch.
# Until a release exists, this script exits and prints the source install.
$ErrorActionPreference = "Stop"

$repo = "lnch-sh/lnch"
$arch = if ($env:PROCESSOR_ARCHITECTURE -eq "ARM64") { "arm64" } else { "amd64" }
$headers = @{ "User-Agent" = "lnch-install" }

try {
    $rel = Invoke-RestMethod -Uri "https://api.github.com/repos/$repo/releases/latest" -Headers $headers
} catch {
    Write-Host "lnch: no GitHub release found for $repo."
    Write-Host "Install from source:"
    Write-Host "  git clone https://github.com/lnch-sh/lnch"
    Write-Host "  cd lnch"
    Write-Host "  go install ./cmd/lnch"
    exit 1
}

$tag = $rel.tag_name
if (-not $tag) {
    Write-Host "lnch: the latest release has no tag."
    exit 1
}

$asset = "lnch_windows_$arch.zip"
$url = "https://github.com/$repo/releases/download/$tag/$asset"
$dest = if ($env:LNCH_BIN) { $env:LNCH_BIN } else { Join-Path $env:LOCALAPPDATA "lnch" }
New-Item -ItemType Directory -Force -Path $dest | Out-Null
$zip = Join-Path $env:TEMP $asset

try {
    Invoke-WebRequest -Uri $url -OutFile $zip -Headers $headers
} catch {
    Write-Host "lnch: could not download $url"
    Write-Host "Install from source: go install ./cmd/lnch"
    exit 1
}

Expand-Archive -Path $zip -DestinationPath $dest -Force
$bin = Join-Path $dest "lnch.exe"
if (-not (Test-Path $bin)) {
    Write-Host "lnch: archive $asset did not contain lnch.exe"
    exit 1
}

Write-Host "lnch installed to $bin"
Write-Host "Add $dest to your PATH if it is not already there."
& $bin version
