<#
.SYNOPSIS
  Writes a build traceability manifest for GoDownloader Windows artifacts, refusing stale or unverifiable artifacts.

.DESCRIPTION
  Records, for each artifact: file name, size and SHA-256 (computed from the actual files). Records the application
  version, the build identity embedded in the desktop executable (--print-build-info), the repository HEAD, whether
  the working tree had uncommitted or untracked changes, the toolchain versions and the build invocation.

  The script refuses (exits non-zero, writes nothing) when:
    - an artifact is missing;
    - no desktop executable is given;
    - the desktop executable is not a well-formed PE image, or is not linked for the Windows GUI subsystem
      (PE subsystem 2); a console-subsystem build (3) opens a console window next to the application. This header
      check runs before the executable is ever started;
    - the desktop executable does not contain the --print-build-info flag (a stale or foreign binary is never
      executed: an old build that does not know the flag would start the full application instead);
    - the provenance probe does not finish within the timeout;
    - the embedded commit differs from the repository HEAD (stale or foreign build);
    - the executable claims a clean build but the working tree is modified (mislabelled build);
    - the installer is older than the executable it should contain (stale installer).

  A dirty build is recorded with releaseCandidate = false; it can never be labelled a clean release.

  This is a traceability record. It is not a signature, and it does not claim bit-for-bit reproducibility.
  Paths written to the manifest are file names only; no developer-local absolute paths are recorded.
#>
param(
    [Parameter(Mandatory = $true)][string]$Version,
    [Parameter(Mandatory = $true)][string[]]$Artifacts,
    [Parameter(Mandatory = $true)][string]$OutFile,
    [string]$Invocation = "",
    [int]$ProbeTimeoutSeconds = 15
)

$ErrorActionPreference = 'Stop'

function Get-CommandOutput([string]$exe, [string[]]$arguments) {
    # Windows PowerShell 5 turns redirected native stderr into errors; do not let them abort the manifest.
    $ErrorActionPreference = 'Continue'
    try {
        # Some tools (e.g. wails3 version) print to stderr; merge both streams.
        $out = & $exe @arguments 2>&1 | ForEach-Object { "$_" }
        if ($LASTEXITCODE -ne 0) { return "" }
        return (($out | Out-String).Trim())
    } catch {
        return ""
    }
}

function Test-ContainsAscii([string]$path, [string]$marker) {
    $bytes = [System.IO.File]::ReadAllBytes($path)
    $needle = [System.Text.Encoding]::ASCII.GetBytes($marker)
    $last = $bytes.Length - $needle.Length
    for ($i = 0; $i -le $last; $i++) {
        if ($bytes[$i] -ne $needle[0]) { continue }
        $match = $true
        for ($j = 1; $j -lt $needle.Length; $j++) {
            if ($bytes[$i + $j] -ne $needle[$j]) { $match = $false; break }
        }
        if ($match) { return $true }
    }
    return $false
}

# Returns the Subsystem field of a PE image's optional header (2 = WINDOWS_GUI, 3 = WINDOWS_CUI console). Only the
# headers are read, every offset is bounds-checked against the file length, and anything that is not a well-formed
# PE32/PE32+ image is refused. The file is never executed.
function Get-PESubsystem([string]$path) {
    $name = Split-Path -Leaf $path
    $stream = [System.IO.File]::Open($path, [System.IO.FileMode]::Open, [System.IO.FileAccess]::Read, [System.IO.FileShare]::Read)
    try {
        $reader = New-Object System.IO.BinaryReader($stream)
        $length = $stream.Length
        if ($length -lt 64) { throw "$name is not a valid PE executable (too small); refusing." }
        if ($reader.ReadUInt16() -ne 0x5A4D) { throw "$name is not a valid PE executable (no MZ header); refusing." }
        $stream.Position = 0x3C
        $peOffset = [int64]$reader.ReadUInt32()
        # PE signature (4) + COFF file header (20) + optional header up to and including Subsystem (68 + 2).
        if ($peOffset -lt 64 -or ($peOffset + 4 + 20 + 70) -gt $length) {
            throw "$name is not a valid PE executable (PE header out of bounds); refusing."
        }
        $stream.Position = $peOffset
        if ($reader.ReadUInt32() -ne 0x00004550) { throw "$name is not a valid PE executable (no PE signature); refusing." }
        $stream.Position = $peOffset + 4 + 16
        if ($reader.ReadUInt16() -lt 70) { throw "$name is not a valid PE executable (optional header too small); refusing." }
        $stream.Position = $peOffset + 24
        $magic = $reader.ReadUInt16()
        if ($magic -ne 0x10B -and $magic -ne 0x20B) { throw "$name is not a valid PE executable (unknown optional header); refusing." }
        # Subsystem is at offset 68 of the optional header in both PE32 and PE32+.
        $stream.Position = $peOffset + 24 + 68
        return [int]$reader.ReadUInt16()
    } finally {
        $stream.Dispose()
    }
}

# Runs the executable's provenance probe with a timeout. Only this script's own child process is ever terminated.
function Get-EmbeddedBuildInfo([string]$path, [int]$timeoutSeconds) {
    $psi = New-Object System.Diagnostics.ProcessStartInfo
    $psi.FileName = $path
    $psi.Arguments = '--print-build-info'
    $psi.UseShellExecute = $false
    $psi.RedirectStandardOutput = $true
    $psi.CreateNoWindow = $true
    $proc = [System.Diagnostics.Process]::Start($psi)
    $stdout = $proc.StandardOutput.ReadToEndAsync()
    if (-not $proc.WaitForExit($timeoutSeconds * 1000)) {
        try { $proc.Kill() } catch { }
        throw "Provenance probe of $(Split-Path -Leaf $path) did not finish within $timeoutSeconds s; refusing."
    }
    if ($proc.ExitCode -ne 0) {
        throw "Provenance probe of $(Split-Path -Leaf $path) failed (exit $($proc.ExitCode)); refusing."
    }
    return ($stdout.Result | ConvertFrom-Json)
}

# With `powershell -File` (as the Taskfile invokes it) a comma list arrives as one string; split it.
$Artifacts = @($Artifacts | ForEach-Object { $_ -split ',' } | ForEach-Object { $_.Trim().Trim('"') } | Where-Object { $_ })

$items = @()
foreach ($path in $Artifacts) {
    if (-not (Test-Path -LiteralPath $path -PathType Leaf)) {
        throw "Artifact not found: $(Split-Path -Leaf $path)"
    }
    $items += Get-Item -LiteralPath $path
}

$appExe = $items | Where-Object { $_.Extension -eq '.exe' -and $_.Name -notmatch '-setup\.exe$' } | Select-Object -First 1
if ($null -eq $appExe) {
    throw "No desktop executable among the artifacts; its embedded build identity is required."
}
$subsystem = Get-PESubsystem $appExe.FullName
if ($subsystem -ne 2) {
    $kind = if ($subsystem -eq 3) { 'console, WINDOWS_CUI' } else { 'not WINDOWS_GUI' }
    throw "$($appExe.Name) is not a Windows GUI executable (PE subsystem $subsystem, $kind; expected 2, WINDOWS_GUI); refusing."
}
if (-not (Test-ContainsAscii $appExe.FullName '--print-build-info')) {
    throw "$($appExe.Name) does not support --print-build-info (stale or foreign build); refusing to execute it."
}
$embedded = Get-EmbeddedBuildInfo $appExe.FullName $ProbeTimeoutSeconds
if ($null -eq $embedded -or -not $embedded.commit -or $embedded.commitSource -ne 'injected') {
    throw "$($appExe.Name) has no injected source commit; refusing."
}

$head = Get-CommandOutput 'git' @('rev-parse', 'HEAD')
if (-not $head) { throw "Cannot read the repository HEAD." }
$embeddedCommit = ([string]$embedded.commit).Split('-')[0]
if ($embeddedCommit -ne $head.ToLowerInvariant()) {
    throw "$($appExe.Name) was built from $embeddedCommit, not HEAD $head (stale or foreign build); refusing."
}

$porcelain = Get-CommandOutput 'git' @('status', '--porcelain', '--untracked-files=normal')
$treeModified = [bool]$porcelain
if ($treeModified -and -not [bool]$embedded.dirty) {
    throw "$($appExe.Name) claims a clean build but the working tree has uncommitted or untracked changes; refusing."
}

foreach ($setup in ($items | Where-Object { $_.Name -match '-setup\.exe$' })) {
    if ($setup.LastWriteTimeUtc -lt $appExe.LastWriteTimeUtc) {
        throw "$($setup.Name) is older than $($appExe.Name) (stale installer); refusing."
    }
}

$artifactRecords = @()
foreach ($item in $items) {
    $artifactRecords += [ordered]@{
        fileName = $item.Name
        sizeBytes = $item.Length
        sha256 = (Get-FileHash -LiteralPath $item.FullName -Algorithm SHA256).Hash.ToLowerInvariant()
    }
}

$manifest = [ordered]@{
    schema = 'godownloader.build-manifest/v2'
    application = 'GoDownloader'
    version = $Version
    generatedAtUtc = (Get-Date).ToUniversalTime().ToString('o')
    releaseCandidate = (-not [bool]$embedded.dirty) -and (-not $treeModified)
    source = [ordered]@{
        embeddedBuildInfo = $embedded
        repositoryHead = $head
        workingTreeModified = $treeModified
        embeddedCommitMatchesHead = $true
    }
    toolchain = [ordered]@{
        go = Get-CommandOutput 'go' @('version')
        wails = Get-CommandOutput 'wails3' @('version')
    }
    invocation = $Invocation
    artifacts = $artifactRecords
    notes = @(
        'Traceability record only: not a signature and not a bit-for-bit reproducibility guarantee.',
        'Artifacts are unsigned.'
    )
}

$json = $manifest | ConvertTo-Json -Depth 6
[System.IO.File]::WriteAllText([System.IO.Path]::GetFullPath($OutFile), $json, (New-Object System.Text.UTF8Encoding($false)))
Write-Output "Build manifest written: $(Split-Path -Leaf $OutFile)"
