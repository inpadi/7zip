@echo off
setlocal EnableExtensions

pushd "%~dp0" >nul || (
  echo ERROR: Unable to enter the project directory.
  exit /b 1
)

where go >nul 2>nul
if errorlevel 1 (
  echo ERROR: Go was not found on PATH.
  popd
  exit /b 1
)

if /I "%I7Z_RELEASE_BUILD%"=="1" goto :verify_release_source

echo Building local artifacts from the current worktree.
echo Set I7Z_RELEASE_BUILD=1 to require a clean, exactly tagged release source.
goto :source_verified

:verify_release_source
where git >nul 2>nul
if errorlevel 1 (
  echo ERROR: Git was not found on PATH.
  popd
  exit /b 1
)

git diff --quiet -- .
if errorlevel 1 (
  echo ERROR: Refusing to build release artifacts from a dirty worktree.
  popd
  exit /b 1
)
git diff --cached --quiet -- .
if errorlevel 1 (
  echo ERROR: Refusing to build release artifacts with staged changes.
  popd
  exit /b 1
)
git describe --exact-match --tags HEAD >nul 2>nul
if errorlevel 1 (
  echo ERROR: Release artifacts must be built from an exact source tag.
  popd
  exit /b 1
)

:source_verified

go mod verify
if errorlevel 1 (
  popd
  exit /b 1
)

set "OutName=i7z"
set "BuildArgs=%*"
set "CGO_ENABLED=0"

call :build linux arm
if errorlevel 1 goto :failed
call :build linux arm64
if errorlevel 1 goto :failed
call :build linux 386
if errorlevel 1 goto :failed
call :build linux amd64
if errorlevel 1 goto :failed

call :build windows 386 .exe
if errorlevel 1 goto :failed
call :build windows amd64 .exe native
if errorlevel 1 goto :failed
call :build windows amd64 -portable.exe portable
if errorlevel 1 goto :failed
call :build windows arm64 .exe
if errorlevel 1 goto :failed

call :build darwin amd64
if errorlevel 1 goto :failed
call :build darwin arm64
if errorlevel 1 goto :failed

powershell -NoProfile -Command "$root=(Resolve-Path 'Out').Path; Get-ChildItem 'Out' -Recurse -File | Where-Object Name -ne 'SHA256SUMS' | Sort-Object FullName | ForEach-Object { $relative=$_.FullName.Substring($root.Length+1).Replace('\','/'); '{0}  {1}' -f (Get-FileHash -Algorithm SHA256 -LiteralPath $_.FullName).Hash.ToLowerInvariant(),$relative } | Set-Content -Encoding ascii 'Out\SHA256SUMS'"
if errorlevel 1 goto :failed

echo.
echo All i7z artifacts were built successfully in "%CD%\Out".
popd
exit /b 0

:build
set "GOOS=%~1"
set "GOARCH=%~2"
set "Output=Out\%GOOS%\%GOARCH%\%OutName%%~3"
set "CGO_ENABLED=0"

if /I "%~4"=="native" (
  where gcc >nul 2>nul
  if errorlevel 1 (
    echo ERROR: GCC is required for the accelerated Windows AMD64 artifact.
    exit /b 1
  )
  set "CGO_ENABLED=1"
)

if not exist "Out\%GOOS%\%GOARCH%" mkdir "Out\%GOOS%\%GOARCH%"
if errorlevel 1 exit /b 1

echo Building %GOOS%/%GOARCH%: %Output%
go build %BuildArgs% -trimpath -o "%Output%" ./cmd/7zip
if errorlevel 1 exit /b 1

if /I "%~4"=="native" (
  go tool nm "%Output%" | findstr /C:"LzmaDec_DecodeReal_3" >nul
  if errorlevel 1 (
    echo ERROR: Accelerated artifact is missing the x64 LZMA decoder.
    exit /b 1
  )
  go tool nm "%Output%" | findstr /C:"i7z_lzma_encoder_create" >nul
  if errorlevel 1 (
    echo ERROR: Accelerated artifact is missing the native LZMA encoder.
    exit /b 1
  )
)

if /I "%GOOS%"=="windows" call :sign "%Output%"
if errorlevel 1 exit /b 1
exit /b 0

:sign
if defined GMS_SKIP_SIGN (
  echo Skipping code signing for %~1.
  exit /b 0
)

set "SignScript=%GMS_SIGN_SCRIPT%"
if not defined SignScript set "SignScript=C:\Users\eh\Documents\GitHub\inpadi-codesign\Sign.cmd"
if not exist "%SignScript%" (
  if /I not "%I7Z_RELEASE_BUILD%"=="1" (
    echo Code-signing script not found; leaving local artifact %~1 unsigned.
    exit /b 0
  )
  echo ERROR: Code-signing script not found: %SignScript%
  exit /b 1
)

echo Signing %~1
call "%SignScript%" "%~1"
exit /b %errorlevel%

:failed
set "ExitCode=%errorlevel%"
echo.
echo ERROR: i7z build failed with exit code %ExitCode%.
popd
exit /b %ExitCode%
