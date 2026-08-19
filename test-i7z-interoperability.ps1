<#
.SYNOPSIS
Cross-tests i7z compression and extraction against a reference 7-Zip executable.

.DESCRIPTION
Builds the current i7z source unless -I7zPath is supplied and exercises every
compression method accepted by i7z. Use -SourcePath to benchmark an existing
file or directory; otherwise, the script creates a deterministic test payload.
Each available creation direction is extracted by both applications and compared
using relative paths, sizes, and SHA-256 hashes. Compression and extraction speed,
archive size, requested compression level, compression ratio, and space saved are reported.

.PARAMETER SourcePath
File or directory to benchmark. When omitted, a small deterministic payload is
generated. Directories are packaged once as an uncompressed stream for methods
that accept only one input file; that preparation time is not benchmarked.

.PARAMETER CompressionLevel
Compression level passed to both applications as -mx. Valid values are 0-9 and
the default is 7.

.EXAMPLE
.\test-i7z-interoperability.ps1

.EXAMPLE
.\test-i7z-interoperability.ps1 -SevenZipPath 'C:\Program Files\7-Zip\7z.exe' -KeepArtifacts

.EXAMPLE
.\test-i7z-interoperability.ps1 -SourcePath 'D:\test-data' -CompressionLevel 9
#>
[CmdletBinding()]
param(
    [string]$SourcePath,
    [string]$I7zPath,
    [string]$SevenZipPath = 'C:\Program Files\7-Zip\7z.exe',
    [ValidateRange(0, 9)][int]$CompressionLevel = 7,
    [switch]$KeepArtifacts
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

function Format-CommandArgument {
    param([string]$Value)

    if ($Value -notmatch '[\s"]') {
        return $Value
    }
    return '"' + $Value.Replace('"', '\"') + '"'
}

function Invoke-NativeCommand {
    param(
        [Parameter(Mandatory = $true)][string]$FilePath,
        [Parameter(Mandatory = $true)][string[]]$ArgumentList,
        [Parameter(Mandatory = $true)][string]$WorkingDirectory,
        [Parameter(Mandatory = $true)][string]$Description
    )

    $displayArguments = ($ArgumentList | ForEach-Object { Format-CommandArgument $_ }) -join ' '
    Write-Host ("  {0}: {1} {2}" -f $Description, $FilePath, $displayArguments) -ForegroundColor DarkGray

    $lines = New-Object 'System.Collections.Generic.List[string]'
    Push-Location -LiteralPath $WorkingDirectory
    try {
        # Native stderr is represented as ErrorRecord objects by Windows PowerShell.
        $previousErrorAction = $ErrorActionPreference
        $ErrorActionPreference = 'Continue'
        $stopwatch = [Diagnostics.Stopwatch]::StartNew()
        try {
            & $FilePath @ArgumentList 2>&1 | ForEach-Object {
                [void]$lines.Add($_.ToString())
            }
            $exitCode = $LASTEXITCODE
        }
        finally {
            $stopwatch.Stop()
            $ErrorActionPreference = $previousErrorAction
        }
    }
    finally {
        Pop-Location
    }

    return [pscustomobject]@{
        ExitCode = $exitCode
        Output = $lines -join [Environment]::NewLine
        Elapsed = $stopwatch.Elapsed
    }
}

function Invoke-CheckedNativeCommand {
    param(
        [Parameter(Mandatory = $true)][string]$FilePath,
        [Parameter(Mandatory = $true)][string[]]$ArgumentList,
        [Parameter(Mandatory = $true)][string]$WorkingDirectory,
        [Parameter(Mandatory = $true)][string]$Description
    )

    $result = Invoke-NativeCommand @PSBoundParameters
    if ($result.ExitCode -ne 0) {
        $details = if ([string]::IsNullOrWhiteSpace($result.Output)) { '<no output>' } else { $result.Output }
        throw "${Description} failed with exit code $($result.ExitCode):`n$details"
    }
    return $result
}

function Get-TreeManifest {
    param([Parameter(Mandatory = $true)][string]$Root)

    if (-not (Test-Path -LiteralPath $Root -PathType Container)) {
        throw "Expected extraction directory was not created: $Root"
    }

    $basePath = [IO.Path]::GetFullPath($Root).TrimEnd('\', '/') + [IO.Path]::DirectorySeparatorChar
    return @(
        Get-ChildItem -LiteralPath $Root -Force -Recurse | ForEach-Object {
            $fullName = [IO.Path]::GetFullPath($_.FullName)
            $relativeName = $fullName.Substring($basePath.Length).Replace('\', '/')
            if ($_.PSIsContainer) {
                return 'D|{0}' -f $relativeName
            }
            $hash = (Get-FileHash -LiteralPath $fullName -Algorithm SHA256).Hash
            'F|{0}|{1}|{2}' -f $relativeName, $_.Length, $hash
        } | Sort-Object
    )
}

function Get-PathManifest {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [string]$ArchiveName
    )

    $item = Get-Item -LiteralPath $Path -Force
    $rootName = if ([string]::IsNullOrWhiteSpace($ArchiveName)) { $item.Name } else { $ArchiveName }
    if (-not $item.PSIsContainer) {
        $hash = (Get-FileHash -LiteralPath $item.FullName -Algorithm SHA256).Hash
        return ,('F|{0}|{1}|{2}' -f $rootName, $item.Length, $hash)
    }

    $entries = New-Object 'System.Collections.Generic.List[string]'
    [void]$entries.Add(('D|{0}' -f $rootName))
    $basePath = [IO.Path]::GetFullPath($item.FullName).TrimEnd('\', '/') + [IO.Path]::DirectorySeparatorChar
    Get-ChildItem -LiteralPath $item.FullName -Force -Recurse | ForEach-Object {
        $fullName = [IO.Path]::GetFullPath($_.FullName)
        $relativeName = $fullName.Substring($basePath.Length).Replace('\', '/')
        $entryName = $rootName + '/' + $relativeName
        if ($_.PSIsContainer) {
            [void]$entries.Add(('D|{0}' -f $entryName))
        }
        else {
            $hash = (Get-FileHash -LiteralPath $fullName -Algorithm SHA256).Hash
            [void]$entries.Add(('F|{0}|{1}|{2}' -f $entryName, $_.Length, $hash))
        }
    }
    return @($entries | Sort-Object)
}

function Get-PathByteCount {
    param([Parameter(Mandatory = $true)][string]$Path)

    $item = Get-Item -LiteralPath $Path -Force
    if (-not $item.PSIsContainer) {
        return [uint64]$item.Length
    }

    [uint64]$total = 0
    Get-ChildItem -LiteralPath $item.FullName -File -Force -Recurse | ForEach-Object {
        $total += [uint64]$_.Length
    }
    return $total
}

function Assert-ExtractedPayload {
    param(
        [Parameter(Mandatory = $true)][string[]]$ExpectedManifest,
        [Parameter(Mandatory = $true)][string]$ActualRoot,
        [Parameter(Mandatory = $true)][string]$Description
    )

    $actual = @(Get-TreeManifest $ActualRoot)
    $difference = @(Compare-Object -ReferenceObject $ExpectedManifest -DifferenceObject $actual)
    if ($difference.Count -ne 0) {
        $details = $difference | ForEach-Object { '{0} {1}' -f $_.SideIndicator, $_.InputObject }
        throw "${Description} produced a different payload:`n$($details -join [Environment]::NewLine)"
    }
}

function New-TestPayload {
    param([Parameter(Mandatory = $true)][string]$RunRoot)

    $utf8 = New-Object System.Text.UTF8Encoding($false)

    $containerRoot = Join-Path $RunRoot 'container-source'
    $payloadRoot = Join-Path $containerRoot 'payload'
    $nestedRoot = Join-Path $payloadRoot 'nested'
    [void](New-Item -ItemType Directory -Path $nestedRoot -Force)
    [void](New-Item -ItemType Directory -Path (Join-Path $payloadRoot 'empty-directory') -Force)

    [IO.File]::WriteAllText(
        (Join-Path $payloadRoot 'readme.txt'),
        (('i7z interoperability payload' + [Environment]::NewLine) * 256),
        $utf8
    )
    [IO.File]::WriteAllBytes((Join-Path $nestedRoot 'empty.dat'), [byte[]]@())

    $binary = New-Object byte[] 65537
    for ($index = 0; $index -lt $binary.Length; $index++) {
        $binary[$index] = [byte](($index * 31 + [math]::Floor($index / 251)) % 256)
    }
    [IO.File]::WriteAllBytes((Join-Path $nestedRoot 'binary.bin'), $binary)

    $streamRoot = Join-Path $RunRoot 'stream-source'
    [void](New-Item -ItemType Directory -Path $streamRoot -Force)
    [IO.File]::WriteAllBytes((Join-Path $streamRoot 'payload.bin'), $binary)

    return [pscustomobject]@{
        ContainerPath = $payloadRoot
        StreamPath = Join-Path $streamRoot 'payload.bin'
    }
}

function Initialize-TestPayload {
    param(
        [Parameter(Mandatory = $true)][string]$RunRoot,
        [string]$SourcePath,
        [Parameter(Mandatory = $true)][string]$I7zPath
    )

    if ([string]::IsNullOrWhiteSpace($SourcePath)) {
        $paths = New-TestPayload -RunRoot $RunRoot
        $sourceDescription = 'generated deterministic payload'
    }
    else {
        if (-not (Test-Path -LiteralPath $SourcePath)) {
            throw "Test source was not found: $SourcePath"
        }
        $resolvedSource = (Resolve-Path -LiteralPath $SourcePath).Path
        $sourceItem = Get-Item -LiteralPath $resolvedSource -Force
        if ($sourceItem.PSIsContainer -and $null -eq $sourceItem.Parent) {
            throw "A filesystem root cannot be used as SourcePath: $resolvedSource"
        }

        $streamPath = $resolvedSource
        if ($sourceItem.PSIsContainer) {
            $streamRoot = Join-Path $RunRoot 'stream-source'
            [void](New-Item -ItemType Directory -Path $streamRoot -Force)
            $streamPath = Join-Path $streamRoot ($sourceItem.Name + '.stream')
            Write-Host "Preparing uncompressed TAR for single-stream tests: $streamPath"
            $null = Invoke-CheckedNativeCommand -FilePath $I7zPath `
                -ArgumentList @('a', '-bd', '-y', '-ttar', $streamPath, '--', $sourceItem.Name) `
                -WorkingDirectory $sourceItem.Parent.FullName -Description 'Prepare stream payload'
        }

        $paths = [pscustomobject]@{
            ContainerPath = $resolvedSource
            StreamPath = $streamPath
        }
        $sourceDescription = $resolvedSource
    }

    $containerItem = Get-Item -LiteralPath $paths.ContainerPath -Force
    $streamItem = Get-Item -LiteralPath $paths.StreamPath -Force
    $containerRoot = if ($containerItem.PSIsContainer) {
        $containerItem.Parent.FullName
    }
    else {
        $containerItem.Directory.FullName
    }
    return [pscustomobject]@{
        Description = $sourceDescription
        ContainerPath = $containerItem.FullName
        ContainerRoot = $containerRoot
        ContainerName = $containerItem.Name
        ContainerManifest = @(Get-PathManifest -Path $containerItem.FullName)
        ContainerBytes = Get-PathByteCount -Path $containerItem.FullName
        StreamPath = $streamItem.FullName
        StreamRoot = $streamItem.Directory.FullName
        StreamName = $streamItem.Name
        StreamArchiveName = 'payload.stream'
        StreamManifest = @(Get-PathManifest -Path $streamItem.FullName -ArchiveName 'payload.stream')
        StreamBytes = Get-PathByteCount -Path $streamItem.FullName
    }
}

function Get-ArchiveFileName {
    param(
        [Parameter(Mandatory = $true)]$TestCase,
        [Parameter(Mandatory = $true)][string]$SourceName
    )

    if ($TestCase.PayloadKind -eq 'stream') {
        return $SourceName + '.' + $TestCase.Extension
    }
    return 'payload.' + $TestCase.Extension
}

function Get-I7zCreateArguments {
    param(
        [Parameter(Mandatory = $true)]$TestCase,
        [Parameter(Mandatory = $true)][string]$ArchivePath,
        [Parameter(Mandatory = $true)][string]$SourceName,
        [Parameter(Mandatory = $true)][int]$CompressionLevel
    )

    return @(
        'a', '-bd', '-y', ("-mx={0}" -f $CompressionLevel), ("-m0={0}" -f $TestCase.I7zMethod),
        $ArchivePath, '--', $SourceName
    )
}

function Get-SevenZipCreateArguments {
    param(
        [Parameter(Mandatory = $true)]$TestCase,
        [Parameter(Mandatory = $true)][string]$ArchivePath,
        [Parameter(Mandatory = $true)][string]$SourceName,
        [Parameter(Mandatory = $true)][int]$CompressionLevel
    )

    $arguments = @('a', '-bd', '-y', ("-mx={0}" -f $CompressionLevel), ("-t{0}" -f $TestCase.SevenZipType))
    if (-not [string]::IsNullOrWhiteSpace($TestCase.SevenZipMethod)) {
        $arguments += "-m0=$($TestCase.SevenZipMethod)"
    }
    $arguments += @($ArchivePath, '--', $SourceName)
    return $arguments
}

function Expand-AndVerify {
    param(
        [Parameter(Mandatory = $true)][string]$ToolPath,
        [Parameter(Mandatory = $true)][string]$ToolName,
        [Parameter(Mandatory = $true)][string]$ArchivePath,
        [Parameter(Mandatory = $true)][string]$Destination,
        [Parameter(Mandatory = $true)][string[]]$ExpectedManifest,
        [Parameter(Mandatory = $true)][string]$WorkingDirectory
    )

    [void](New-Item -ItemType Directory -Path $Destination -Force)
    $arguments = @('x', '-bd', '-y', ("-o{0}" -f $Destination), $ArchivePath)
    $result = Invoke-CheckedNativeCommand -FilePath $ToolPath -ArgumentList $arguments `
        -WorkingDirectory $WorkingDirectory -Description "$ToolName extraction"
    Assert-ExtractedPayload -ExpectedManifest $ExpectedManifest -ActualRoot $Destination `
        -Description "$ToolName extraction"
    return $result
}

function New-CompressionMetric {
    param(
        [Parameter(Mandatory = $true)][string]$Method,
        [Parameter(Mandatory = $true)][string]$Archiver,
        [Parameter(Mandatory = $true)][int]$Level,
        [Parameter(Mandatory = $true)][uint64]$InputBytes,
        [Parameter(Mandatory = $true)][uint64]$ArchiveBytes,
        [Parameter(Mandatory = $true)][timespan]$Elapsed
    )

    $seconds = [math]::Max($Elapsed.TotalSeconds, 0.000001)
    $ratio = if ($InputBytes -eq 0) { 'n/a' } else { '{0:N2}' -f (($ArchiveBytes / $InputBytes) * 100) }
    $saved = if ($InputBytes -eq 0) { 'n/a' } else { '{0:N2}' -f ((1 - ($ArchiveBytes / $InputBytes)) * 100) }
    return [pscustomobject][ordered]@{
        Method = $Method
        Archiver = $Archiver
        Level = $Level
        Seconds = '{0:N3}' -f $Elapsed.TotalSeconds
        'Speed MiB/s' = '{0:N2}' -f (($InputBytes / 1MB) / $seconds)
        InputBytes = $InputBytes
        ArchiveBytes = $ArchiveBytes
        'Ratio %' = $ratio
        'Saved %' = $saved
    }
}

function New-ExtractionMetric {
    param(
        [Parameter(Mandatory = $true)][string]$Method,
        [Parameter(Mandatory = $true)][string]$ArchiveCreatedBy,
        [Parameter(Mandatory = $true)][string]$Extractor,
        [Parameter(Mandatory = $true)][uint64]$ArchiveBytes,
        [Parameter(Mandatory = $true)][uint64]$OutputBytes,
        [Parameter(Mandatory = $true)][timespan]$Elapsed
    )

    $seconds = [math]::Max($Elapsed.TotalSeconds, 0.000001)
    $saved = if ($OutputBytes -eq 0) { 'n/a' } else { '{0:N2}' -f ((1 - ($ArchiveBytes / $OutputBytes)) * 100) }
    return [pscustomobject][ordered]@{
        Method = $Method
        'Archive by' = $ArchiveCreatedBy
        Extractor = $Extractor
        Seconds = '{0:N3}' -f $Elapsed.TotalSeconds
        'Speed MiB/s' = '{0:N2}' -f (($OutputBytes / 1MB) / $seconds)
        OutputBytes = $OutputBytes
        'Saved %' = $saved
    }
}

$testCases = @(
    [pscustomobject]@{ Name = 'copy';    Extension = '7z';  PayloadKind = 'container'; I7zMethod = 'copy';    SevenZipType = '7z';    SevenZipMethod = 'Copy';    MethodPattern = '(?im)^Method = Copy\s*$';    OptionalSevenZipCreate = $false },
    [pscustomobject]@{ Name = 'store';   Extension = '7z';  PayloadKind = 'container'; I7zMethod = 'store';   SevenZipType = '7z';    SevenZipMethod = 'Copy';    MethodPattern = '(?im)^Method = Copy\s*$';    OptionalSevenZipCreate = $false },
    [pscustomobject]@{ Name = 'lzma';    Extension = '7z';  PayloadKind = 'container'; I7zMethod = 'lzma';    SevenZipType = '7z';    SevenZipMethod = 'LZMA';    MethodPattern = '(?im)^Method = LZMA:';          OptionalSevenZipCreate = $false },
    [pscustomobject]@{ Name = 'lzma2';   Extension = '7z';  PayloadKind = 'container'; I7zMethod = 'lzma2';   SevenZipType = '7z';    SevenZipMethod = 'LZMA2';   MethodPattern = '(?im)^Method = LZMA2:';         OptionalSevenZipCreate = $false },
    [pscustomobject]@{ Name = 'deflate'; Extension = 'zip'; PayloadKind = 'container'; I7zMethod = 'deflate'; SevenZipType = 'zip';   SevenZipMethod = 'Deflate'; MethodPattern = '(?im)^Method = Deflate\s*$'; OptionalSevenZipCreate = $false },
    [pscustomobject]@{ Name = 'bzip2';   Extension = 'bz2'; PayloadKind = 'stream';    I7zMethod = 'bzip2';   SevenZipType = 'bzip2'; SevenZipMethod = '';        MethodPattern = '(?im)^Type = bzip2\s*$';     OptionalSevenZipCreate = $false },
    [pscustomobject]@{ Name = 'xz';      Extension = 'xz';  PayloadKind = 'stream';    I7zMethod = 'xz';      SevenZipType = 'xz';    SevenZipMethod = '';        MethodPattern = '(?im)^Method = LZMA2:';         OptionalSevenZipCreate = $false },
    [pscustomobject]@{ Name = 'zstd';    Extension = 'zst'; PayloadKind = 'stream';    I7zMethod = 'zstd';    SevenZipType = 'zstd';  SevenZipMethod = '';        MethodPattern = '(?im)^Type = zstd\s*$';      OptionalSevenZipCreate = $true }
)

$runRoot = Join-Path ([IO.Path]::GetTempPath()) ('i7z-cross-' + [guid]::NewGuid().ToString('N'))
$markerPath = Join-Path $runRoot '.i7z-test-root'
$failed = 0
$passed = 0
$skippedDirections = 0
$compressionMetrics = New-Object 'System.Collections.Generic.List[object]'
$extractionMetrics = New-Object 'System.Collections.Generic.List[object]'

try {
    [void](New-Item -ItemType Directory -Path $runRoot -Force)
    [IO.File]::WriteAllText($markerPath, 'Created by test-i7z-interoperability.ps1')

    if (-not (Test-Path -LiteralPath $SevenZipPath -PathType Leaf)) {
        throw "7-Zip reference executable was not found: $SevenZipPath"
    }
    $resolvedSevenZip = (Resolve-Path -LiteralPath $SevenZipPath).Path

    if ([string]::IsNullOrWhiteSpace($I7zPath)) {
        $go = Get-Command go -ErrorAction SilentlyContinue
        if ($null -eq $go) {
            throw 'Go was not found on PATH. Install Go or pass -I7zPath to an existing i7z executable.'
        }
        $resolvedI7z = Join-Path $runRoot 'i7z-under-test.exe'
        $null = Invoke-CheckedNativeCommand -FilePath $go.Source `
            -ArgumentList @('build', '-trimpath', '-o', $resolvedI7z, './cmd/7zip') `
            -WorkingDirectory $PSScriptRoot -Description 'Build i7z'
    }
    else {
        if (-not (Test-Path -LiteralPath $I7zPath -PathType Leaf)) {
            throw "i7z executable was not found: $I7zPath"
        }
        $resolvedI7z = (Resolve-Path -LiteralPath $I7zPath).Path
    }

    Write-Host "i7z:  $resolvedI7z"
    Write-Host "7-Zip: $resolvedSevenZip"
    Write-Host "Work:  $runRoot"

    $payload = Initialize-TestPayload -RunRoot $runRoot -SourcePath $SourcePath -I7zPath $resolvedI7z
    Write-Host "Source: $($payload.Description)"
    Write-Host "Compression level: $CompressionLevel"

    foreach ($testCase in $testCases) {
        Write-Host "`n[$($testCase.Name)]" -ForegroundColor Cyan
        $caseRoot = Join-Path $runRoot $testCase.Name
        $fromI7z = Join-Path $caseRoot 'from-i7z'
        $fromSevenZip = Join-Path $caseRoot 'from-7zip'
        [void](New-Item -ItemType Directory -Path $fromI7z -Force)
        [void](New-Item -ItemType Directory -Path $fromSevenZip -Force)

        if ($testCase.PayloadKind -eq 'stream') {
            $sourceRoot = $payload.StreamRoot
            $sourceName = $payload.StreamName
            $archiveEntryName = $payload.StreamArchiveName
            $expectedManifest = $payload.StreamManifest
            [uint64]$inputBytes = $payload.StreamBytes
        }
        else {
            $sourceRoot = $payload.ContainerRoot
            $sourceName = $payload.ContainerName
            $archiveEntryName = $sourceName
            $expectedManifest = $payload.ContainerManifest
            [uint64]$inputBytes = $payload.ContainerBytes
        }

        $archiveName = Get-ArchiveFileName -TestCase $testCase -SourceName $archiveEntryName
        $i7zArchive = Join-Path $fromI7z $archiveName
        $sevenZipArchive = Join-Path $fromSevenZip $archiveName

        try {
            $i7zCreateArguments = Get-I7zCreateArguments -TestCase $testCase -ArchivePath $i7zArchive `
                -SourceName $sourceName -CompressionLevel $CompressionLevel
            $i7zCreateResult = Invoke-CheckedNativeCommand -FilePath $resolvedI7z -ArgumentList $i7zCreateArguments `
                -WorkingDirectory $sourceRoot -Description 'i7z creation'
            [uint64]$i7zArchiveBytes = (Get-Item -LiteralPath $i7zArchive).Length
            [void]$compressionMetrics.Add((New-CompressionMetric -Method $testCase.Name -Archiver 'i7z' `
                -Level $CompressionLevel -InputBytes $inputBytes `
                -ArchiveBytes $i7zArchiveBytes -Elapsed $i7zCreateResult.Elapsed))

            $listResult = Invoke-CheckedNativeCommand -FilePath $resolvedSevenZip `
                -ArgumentList @('l', '-bd', '-slt', $i7zArchive) `
                -WorkingDirectory $caseRoot -Description '7-Zip method inspection'
            if ($listResult.Output -notmatch $testCase.MethodPattern) {
                throw "7-Zip did not report the expected $($testCase.Name) method for the i7z archive:`n$($listResult.Output)"
            }

            $i7zExtractsI7z = Expand-AndVerify -ToolPath $resolvedI7z -ToolName 'i7z' -ArchivePath $i7zArchive `
                -Destination (Join-Path $caseRoot 'i7z-extracts-i7z') -ExpectedManifest $expectedManifest `
                -WorkingDirectory $caseRoot
            [void]$extractionMetrics.Add((New-ExtractionMetric -Method $testCase.Name `
                -ArchiveCreatedBy 'i7z' -Extractor 'i7z' -ArchiveBytes $i7zArchiveBytes `
                -OutputBytes $inputBytes -Elapsed $i7zExtractsI7z.Elapsed))

            $sevenZipExtractsI7z = Expand-AndVerify -ToolPath $resolvedSevenZip -ToolName '7-Zip' -ArchivePath $i7zArchive `
                -Destination (Join-Path $caseRoot '7zip-extracts-i7z') -ExpectedManifest $expectedManifest `
                -WorkingDirectory $caseRoot
            [void]$extractionMetrics.Add((New-ExtractionMetric -Method $testCase.Name `
                -ArchiveCreatedBy 'i7z' -Extractor '7-Zip' -ArchiveBytes $i7zArchiveBytes `
                -OutputBytes $inputBytes -Elapsed $sevenZipExtractsI7z.Elapsed))

            $sevenZipCreateArguments = Get-SevenZipCreateArguments -TestCase $testCase `
                -ArchivePath $sevenZipArchive -SourceName $sourceName -CompressionLevel $CompressionLevel
            $sevenZipCreateResult = Invoke-NativeCommand -FilePath $resolvedSevenZip `
                -ArgumentList $sevenZipCreateArguments -WorkingDirectory $sourceRoot `
                -Description '7-Zip creation'

            if ($sevenZipCreateResult.ExitCode -ne 0) {
                if (-not $testCase.OptionalSevenZipCreate) {
                    $details = if ([string]::IsNullOrWhiteSpace($sevenZipCreateResult.Output)) { '<no output>' } else { $sevenZipCreateResult.Output }
                    throw "7-Zip creation failed with exit code $($sevenZipCreateResult.ExitCode):`n$details"
                }
                $skippedDirections++
                Write-Host '  SKIP: this 7-Zip build cannot create Zstandard streams; i7z output was still verified by both tools.' -ForegroundColor Yellow
            }
            else {
                [uint64]$sevenZipArchiveBytes = (Get-Item -LiteralPath $sevenZipArchive).Length
                [void]$compressionMetrics.Add((New-CompressionMetric -Method $testCase.Name -Archiver '7-Zip' `
                    -Level $CompressionLevel -InputBytes $inputBytes `
                    -ArchiveBytes $sevenZipArchiveBytes -Elapsed $sevenZipCreateResult.Elapsed))

                $sevenZipExtractsSevenZip = Expand-AndVerify -ToolPath $resolvedSevenZip -ToolName '7-Zip' -ArchivePath $sevenZipArchive `
                    -Destination (Join-Path $caseRoot '7zip-extracts-7zip') -ExpectedManifest $expectedManifest `
                    -WorkingDirectory $caseRoot
                [void]$extractionMetrics.Add((New-ExtractionMetric -Method $testCase.Name `
                    -ArchiveCreatedBy '7-Zip' -Extractor '7-Zip' -ArchiveBytes $sevenZipArchiveBytes `
                    -OutputBytes $inputBytes -Elapsed $sevenZipExtractsSevenZip.Elapsed))

                $i7zExtractsSevenZip = Expand-AndVerify -ToolPath $resolvedI7z -ToolName 'i7z' -ArchivePath $sevenZipArchive `
                    -Destination (Join-Path $caseRoot 'i7z-extracts-7zip') -ExpectedManifest $expectedManifest `
                    -WorkingDirectory $caseRoot
                [void]$extractionMetrics.Add((New-ExtractionMetric -Method $testCase.Name `
                    -ArchiveCreatedBy '7-Zip' -Extractor 'i7z' -ArchiveBytes $sevenZipArchiveBytes `
                    -OutputBytes $inputBytes -Elapsed $i7zExtractsSevenZip.Elapsed))
            }

            $passed++
            Write-Host "  PASS: $($testCase.Name)" -ForegroundColor Green
        }
        catch {
            $failed++
            Write-Host "  FAIL: $($testCase.Name)" -ForegroundColor Red
            Write-Host $_.Exception.Message -ForegroundColor Red
        }
    }

    Write-Host "`nSummary: $passed passed, $failed failed, $skippedDirections unsupported reference-creation direction(s)."
    Write-Host "`nCompression performance" -ForegroundColor Cyan
    $compressionMetrics | Format-Table -AutoSize | Out-Host
    Write-Host 'Saved % is 100 minus the archive-to-uncompressed-size ratio; negative values mean the archive is larger.' -ForegroundColor DarkGray
    Write-Host "`nExtraction performance" -ForegroundColor Cyan
    $extractionMetrics | Format-Table -AutoSize | Out-Host
    if ($failed -ne 0) {
        throw "$failed interoperability test case(s) failed."
    }
}
catch {
    Write-Host "`nERROR: $($_.Exception.Message)" -ForegroundColor Red
    exit 1
}
finally {
    if ($KeepArtifacts) {
        Write-Host "Artifacts kept at: $runRoot"
    }
    elseif ((Test-Path -LiteralPath $markerPath -PathType Leaf) -and
            ([IO.Path]::GetFileName($runRoot) -match '^i7z-cross-[0-9a-f]{32}$')) {
        Remove-Item -LiteralPath $runRoot -Recurse -Force
    }
}

exit 0
