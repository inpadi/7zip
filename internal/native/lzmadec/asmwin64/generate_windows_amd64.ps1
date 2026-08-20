[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'

$packageRoot = $PSScriptRoot
$assemblyRoot = [IO.Path]::GetFullPath((Join-Path $packageRoot '..\..\sdk7z\asm\x86'))
$source = Join-Path $assemblyRoot 'LzmaDecOpt.asm'
$rawObject = Join-Path $packageRoot 'lzma_dec_opt_windows_amd64.obj'
$systemObject = Join-Path $packageRoot 'lzma_dec_opt_windows_amd64.syso'

if (-not (Get-Command jwasm -ErrorAction SilentlyContinue)) {
    throw 'jwasm is required to assemble the 7-Zip decoder loop'
}
if (-not (Get-Command objcopy -ErrorAction SilentlyContinue)) {
    throw 'GNU objcopy is required to make the COFF object deterministic'
}
if (-not (Test-Path -LiteralPath $source -PathType Leaf)) {
    throw "7-Zip assembly source was not found: $source"
}

Push-Location -LiteralPath $assemblyRoot
try {
    # Some JWasm Windows builds return 1 after successfully writing a valid
    # object. Validate the object itself instead of trusting that exit code.
    & jwasm -nologo -win64 "-Fo=$rawObject" 'LzmaDecOpt.asm'
}
finally {
    Pop-Location
}

if (-not (Test-Path -LiteralPath $rawObject -PathType Leaf)) {
    throw 'jwasm did not produce the expected decoder object'
}

& objcopy $rawObject $systemObject
if ($LASTEXITCODE -ne 0) {
    throw "objcopy failed with exit code $LASTEXITCODE"
}

Remove-Item -LiteralPath $rawObject
Write-Host "Generated $systemObject"
