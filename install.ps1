# Installs hqdb from a GitHub release on Windows.
#
#   irm https://raw.githubusercontent.com/Snarap1/hqdb/main/install.ps1 | iex
#
# Downloads hqdb_windows_<arch>.zip, verifies it against checksums.txt and puts
# hqdb.exe in %LOCALAPPDATA%\Programs\hqdb, adding that directory
# to the user PATH. Re-running upgrades in place. No admin rights needed.
# Works in Windows PowerShell 5.1 and PowerShell 7.
#
# Environment overrides:
#   HQDB_VERSION       release tag to install (default: the latest release)
#   HQDB_INSTALL_DIR   install directory (default: %LOCALAPPDATA%\Programs\hqdb)
#   HQDB_REPO          owner/name of the GitHub repo to install from (default: Snarap1/hqdb)

$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'  # Invoke-WebRequest is very slow with the progress bar in 5.1

& {
    if (-not [Environment]::Is64BitOperatingSystem) {
        throw 'hqdb install: only 64-bit Windows (amd64, arm64) builds are published.'
    }
    # PROCESSOR_ARCHITEW6432 is set when a 32-bit shell runs on a 64-bit OS.
    $cpu = if ($env:PROCESSOR_ARCHITEW6432) { $env:PROCESSOR_ARCHITEW6432 } else { $env:PROCESSOR_ARCHITECTURE }
    $arch = if ($cpu -eq 'ARM64') { 'arm64' } else { 'amd64' }

    # Windows PowerShell 5.1 may default to TLS 1.0, which GitHub rejects.
    [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12

    $repo = if ($env:HQDB_REPO) { $env:HQDB_REPO } else { 'Snarap1/hqdb' }
    $asset = "hqdb_windows_$arch.zip"
    $base = if ($env:HQDB_VERSION) {
        "https://github.com/$repo/releases/download/$($env:HQDB_VERSION)"
    } else {
        "https://github.com/$repo/releases/latest/download"
    }

    $dir = if ($env:HQDB_INSTALL_DIR) { $env:HQDB_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA 'Programs\hqdb' }
    New-Item -ItemType Directory -Force -Path $dir | Out-Null
    $tmp = Join-Path ([IO.Path]::GetTempPath()) ("hqdb-" + [Guid]::NewGuid())
    New-Item -ItemType Directory -Force -Path $tmp | Out-Null

    Write-Host "hqdb install: downloading $base/$asset"
    try {
        $zip = Join-Path $tmp $asset
        Invoke-WebRequest -UseBasicParsing -Uri "$base/$asset" -OutFile $zip
        $sums = (Invoke-WebRequest -UseBasicParsing -Uri "$base/checksums.txt").Content
        if ($sums -is [byte[]]) { $sums = [Text.Encoding]::UTF8.GetString($sums) }

        $line = $sums -split "`r?`n" | Where-Object { $_ -match "^\s*([0-9a-fA-F]{64})\s+\*?$([regex]::Escape($asset))\s*$" } | Select-Object -First 1
        if (-not $line) { throw "hqdb install: $asset not listed in checksums.txt" }
        $expected = ($line -split '\s+')[0].ToLowerInvariant()
        $actual = (Get-FileHash -Algorithm SHA256 -Path $zip).Hash.ToLowerInvariant()
        if ($actual -ne $expected) {
            throw "hqdb install: checksum mismatch for $asset (expected $expected, got $actual)"
        }

        # Match the entry by file name, not path, so a zip layout change
        # cannot break the install.
        Add-Type -AssemblyName System.IO.Compression.FileSystem
        $archive = [IO.Compression.ZipFile]::OpenRead($zip)
        try {
            foreach ($name in @('hqdb.exe')) {
                $entry = $archive.Entries | Where-Object { $_.Name -eq $name } | Select-Object -First 1
                if (-not $entry) { throw "hqdb install: $name not found in $asset" }
                $target = Join-Path $dir $name
                $part = "$target.download"
                [IO.Compression.ZipFileExtensions]::ExtractToFile($entry, $part, $true)
                Move-Item -Force -Path $part -Destination $target
                Write-Host "hqdb install: installed $target"
            }
        } finally {
            $archive.Dispose()
        }
    } finally {
        Remove-Item -Recurse -Force -ErrorAction SilentlyContinue -Path $tmp
    }

    # Read the user PATH from the registry rather than $env:Path, which also
    # holds the machine PATH and would get duplicated into the user scope.
    $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
    $entries = @($userPath -split ';' | Where-Object { $_ })
    if ($entries -notcontains $dir) {
        [Environment]::SetEnvironmentVariable('Path', (($entries + $dir) -join ';'), 'User')
        Write-Host "hqdb install: added $dir to the user PATH"
    }
    if (@($env:Path -split ';') -notcontains $dir) {
        $env:Path = "$env:Path;$dir"
    }

    Write-Host 'hqdb install: next: run hqdb check, then open the terminal UI with hqdb'
    Write-Host "hqdb install: for an AI agent: claude mcp add hqdb -- $(Join-Path $dir 'hqdb.exe') mcp"
    Write-Host 'hqdb install: open a new terminal so other shells pick up the PATH change.'
}
