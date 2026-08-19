$ErrorActionPreference = 'Stop'

# Install-ChocolateyZipPackage records what it extracted; this removes it and
# the shim. The user's config and token are deliberately left alone — they
# belong to the user and to the `bao` CLI, not to this package.
$toolsDir = Split-Path -Parent $MyInvocation.MyCommand.Definition
Uninstall-ChocolateyZipPackage -PackageName 'baobar' -ZipFileName "baobar_$($env:ChocolateyPackageVersion)_windows_amd64.zip"
Remove-Item (Join-Path $toolsDir 'baobar.exe.gui') -ErrorAction SilentlyContinue

# Remove what this package created. The user's config and token stay put.
$startMenu = [Environment]::GetFolderPath('CommonPrograms')
Remove-Item (Join-Path $startMenu 'Baobar.lnk') -ErrorAction SilentlyContinue
