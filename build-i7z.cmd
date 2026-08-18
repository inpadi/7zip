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
call :build windows amd64 .exe
if errorlevel 1 goto :failed
call :build windows arm64 .exe
if errorlevel 1 goto :failed

call :build darwin amd64
if errorlevel 1 goto :failed
call :build darwin arm64
if errorlevel 1 goto :failed

echo.
echo All GMS clients were built successfully in "%CD%\Out".
popd
exit /b 0

:build
set "GOOS=%~1"
set "GOARCH=%~2"
set "Output=Out\%GOOS%\%GOARCH%\%OutName%%~3"

if not exist "Out\%GOOS%\%GOARCH%" mkdir "Out\%GOOS%\%GOARCH%"
if errorlevel 1 exit /b 1

echo Building %GOOS%/%GOARCH%: %Output%
go build %BuildArgs% -trimpath -o "%Output%" ./cmd/7zip
if errorlevel 1 exit /b 1

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
  echo Code-signing script not found; leaving %~1 unsigned.
  exit /b 0
)

echo Signing %~1
call "%SignScript%" "%~1"
exit /b %errorlevel%

:failed
set "ExitCode=%errorlevel%"
echo.
echo ERROR: GMS client build failed with exit code %ExitCode%.
popd
exit /b %ExitCode%
