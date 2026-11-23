@echo off
setlocal
cd /d "%~dp0"
set "arch=%PROCESSOR_ARCHITECTURE%"
if defined PROCESSOR_ARCHITEW6432 set "arch=%PROCESSOR_ARCHITEW6432%"
set "payload=codex-usage-windows-amd64.exe"
if /i "%arch%"=="ARM64" set "payload=codex-usage-windows-arm64.exe"
set "target=%LOCALAPPDATA%\Programs\Codex Usage Tracker"
if not exist "%target%" mkdir "%target%"
if not exist "%target%\Codex Usage.exe" (
  copy /Y "%payload%" "%target%\Codex Usage.exe" >nul
  if errorlevel 1 goto failed
)
set "data=%APPDATA%\Codex Usage Tracker"
if not exist "%data%" mkdir "%data%"
copy /Y "enrollment.json" "%data%\install-enrollment.json" >nul
if errorlevel 1 goto failed
start "" "%target%\Codex Usage.exe" -install-login
exit /b 0
:failed
echo Could not install Codex Usage. Check access to your local Programs folder.
pause
exit /b 1
