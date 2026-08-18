$ErrorActionPreference = 'Stop'

$toolsDir = Split-Path -Parent $MyInvocation.MyCommand.Definition
$version  = $env:ChocolateyPackageVersion

# The release archive is the same artifact published on GitHub, verified by the
# checksum this package was built with. Chocolatey aborts the install if it does
# not match, so a tampered or truncated download cannot reach the user.
$packageArgs = @{
  packageName    = 'baobar'
  unzipLocation  = $toolsDir
  url64bit       = "https://github.com/BrutalSystems/baobar/releases/download/v$version/baobar_${version}_windows_amd64.zip"
  checksum64     = '__CHECKSUM64__'
  checksumType64 = 'sha256'
}

Install-ChocolateyZipPackage @packageArgs

# Baobar is a tray application, not a console tool. Without this marker
# Chocolatey's shim waits for the process to exit and holds the console open;
# with it the shim launches and returns.
New-Item -ItemType File -Path (Join-Path $toolsDir 'baobar.exe.gui') -Force | Out-Null

# A tray app has no console entry point and no installer-created launcher, so
# without this there is nothing in the Start Menu and nothing to pin: the only
# way to start Baobar is to type its path.
#
# Wrapped in try/catch on purpose. This script runs under
# $ErrorActionPreference = 'Stop', and CommonPrograms (the all-users Start Menu)
# needs elevation — which Chocolatey normally has but is not guaranteed to. An
# unguarded failure here would fail the whole package *after* the binary is
# already installed and working, turning a cosmetic problem into a broken
# install. A missing shortcut is worth a warning, not an abort.
try {
  $startMenu = [Environment]::GetFolderPath('CommonPrograms')
  Install-ChocolateyShortcut `
    -ShortcutFilePath (Join-Path $startMenu 'Baobar.lnk') `
    -TargetPath (Join-Path $toolsDir 'baobar.exe') `
    -WorkingDirectory $toolsDir `
    -Description 'Menu bar indicator for OpenBao login state and token expiry'
} catch {
  Write-Warning "Baobar installed, but the Start Menu shortcut could not be created: $_"
}
