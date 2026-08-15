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
