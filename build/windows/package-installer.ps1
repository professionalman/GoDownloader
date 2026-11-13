<#
.SYNOPSIS
  Packages the GoDownloader NSIS installer from an explicitly selected output directory.

.DESCRIPTION
  The executable is read from, and the installer is written to, the same directory: -BinDir (the Taskfile passes
  {{.BIN_DIR}}, the directory the desktop build wrote to). The directory is resolved to an absolute path and passed
  to makensis as /DBIN_DIR, so NSIS can never fall back to another folder (installer.nsi has no default).

  The script refuses (exits non-zero) before running makensis when the output directory or the executable is
  missing, and fails if makensis fails or does not produce a fresh installer in the selected directory. Nothing
  outside -BinDir is read as payload or written.
#>
param(
    [Parameter(Mandatory = $true)][string]$BinDir,
    [string]$AppName = 'GoDownloader',
    [string]$InstallerName = 'GoDownloader-0.9.1-beta-windows-amd64-setup.exe',
    [string]$NsisScript = 'build/windows/installer.nsi',
    [string]$Makensis = ''
)

$ErrorActionPreference = 'Stop'

if (-not (Test-Path -LiteralPath $BinDir -PathType Container)) {
    throw "Output directory '$BinDir' does not exist; refusing to package."
}
$bin = (Resolve-Path -LiteralPath $BinDir).ProviderPath.TrimEnd('\')
$exe = Join-Path $bin "$AppName.exe"
if (-not (Test-Path -LiteralPath $exe -PathType Leaf)) {
    throw "$AppName.exe was not found in the selected output directory; refusing to package."
}
if (-not (Test-Path -LiteralPath $NsisScript -PathType Leaf)) {
    throw "NSIS script '$NsisScript' not found."
}

if (-not $Makensis) {
    $cmd = Get-Command makensis -ErrorAction SilentlyContinue
    if ($cmd) {
        $Makensis = $cmd.Source
    } else {
        foreach ($candidate in @("${env:ProgramFiles(x86)}\NSIS\makensis.exe", "$env:ProgramFiles\NSIS\makensis.exe")) {
            if ($candidate -and (Test-Path -LiteralPath $candidate -PathType Leaf)) { $Makensis = $candidate; break }
        }
    }
}
if (-not $Makensis -or -not (Test-Path -LiteralPath $Makensis -PathType Leaf)) {
    throw 'makensis not found.'
}

$installer = Join-Path $bin $InstallerName
$startedUtc = (Get-Date).ToUniversalTime().AddSeconds(-2)

& $Makensis "/DBIN_DIR=$bin" $NsisScript
if ($LASTEXITCODE -ne 0) {
    throw "makensis failed with exit code $LASTEXITCODE."
}
if (-not (Test-Path -LiteralPath $installer -PathType Leaf)) {
    throw "makensis did not produce $InstallerName in the selected output directory."
}
if ((Get-Item -LiteralPath $installer).LastWriteTimeUtc -lt $startedUtc) {
    throw "$InstallerName in the selected output directory was not written by this packaging run."
}
Write-Output "Installer packaged: $InstallerName"
