@echo off
setlocal
cd /d "%~dp0"
set "arch=%PROCESSOR_ARCHITECTURE%"
if defined PROCESSOR_ARCHITEW6432 set "arch=%PROCESSOR_ARCHITEW6432%"
set "payload=TClash Preview Windows x64.exe"
if /i "%arch%"=="ARM64" set "payload=TClash Preview Windows arm64.exe"
set "target=%LOCALAPPDATA%\TClash Preview\tclash-preview.exe"
set "legacy=%LOCALAPPDATA%\Programs\Codex Usage Tracker\Codex Usage.exe"
set "collector=%LOCALAPPDATA%\TClash Preview\tclash-collector.exe"
set "data=%APPDATA%\TClash Preview\collector"
set "handlers=%APPDATA%\local.tclash.preview\previous-url-handlers"
set "migration=0"
if exist "reuse-b.txt" set "migration=1"
if "%migration%"=="1" if exist "enrollment.json" goto failed
if "%migration%"=="0" if not exist "enrollment.json" goto failed
if "%migration%"=="1" if not exist "%APPDATA%\Codex Usage Tracker\tracker.json" goto nolegacy
if "%migration%"=="1" if not exist "%legacy%" goto nolegacy
rem Do not interrupt a running TClash or replace an in-use proxy.
powershell -NoProfile -NonInteractive -Command "$p=Join-Path $env:LOCALAPPDATA 'TClash Preview\clash-preview.exe'; if (Get-Process -Name 'tclash-preview' -ErrorAction SilentlyContinue | Where-Object { $_.Path -ieq $p }) { exit 1 }" >nul 2>nul
if errorlevel 1 goto tclashrunning
if "%migration%"=="0" (
  powershell -NoProfile -NonInteractive -Command "$p=Join-Path $env:LOCALAPPDATA 'Programs\Codex Usage Tracker\Codex Usage.exe'; if (Get-Process -Name 'Codex Usage' -ErrorAction SilentlyContinue | Where-Object { $_.Path -ieq $p }) { exit 1 }" >nul 2>nul
  if errorlevel 1 goto oldrunning
  if exist "%data%\tracker.json" goto olddata
  if exist "%data%\use-legacy-tracker" goto olddata
  if exist "%APPDATA%\Codex Usage Tracker\tracker.json" goto olddata
)
if not exist "%handlers%\prepared" (
  if not exist "%handlers%" mkdir "%handlers%"
  for %%S in (clash clash-verge) do (
    reg export "HKCU\Software\Classes\%%S" "%handlers%\%%S.reg" /y >nul 2>nul
    if errorlevel 1 type nul > "%handlers%\%%S.absent"
  )
  type nul > "%handlers%\prepared"
)
start /wait "" "%payload%" /S
if errorlevel 1 goto failed
if not exist "%target%" goto failed
if not exist "%collector%" goto failed
"%collector%" -build-info | findstr /x /c:"tclash-collector-device-reset-v1" >nul
if errorlevel 1 goto wrongversion
if not exist "%data%" mkdir "%data%"
if "%migration%"=="0" (
  powershell -NoProfile -NonInteractive -Command "$ErrorActionPreference='Stop'; $k='HKCU:\Software\Microsoft\Windows\CurrentVersion\Run'; $v=(Get-ItemProperty -LiteralPath $k -Name 'Codex Usage Tracker' -ErrorAction SilentlyContinue).'Codex Usage Tracker'; $p=Join-Path $env:LOCALAPPDATA 'Programs\Codex Usage Tracker\Codex Usage.exe'; if ($v) { if ($v.IndexOf($p, [System.StringComparison]::OrdinalIgnoreCase) -lt 0) { throw 'Unrecognized legacy startup command' }; Remove-ItemProperty -LiteralPath $k -Name 'Codex Usage Tracker' -ErrorAction Stop }" >nul 2>nul
  if errorlevel 1 goto startupblocked
  copy /Y "enrollment.json" "%data%\install-enrollment.json" >nul
  if errorlevel 1 goto failed
  start "" "%target%"
  exit /b 0
)
rem Do not interrupt an already running TClash, which may be providing a proxy.
powershell -NoProfile -NonInteractive -Command "$p=Join-Path $env:LOCALAPPDATA 'TClash Preview\tclash-preview.exe'; if (Get-Process -Name 'tclash-preview' -ErrorAction SilentlyContinue | Where-Object { $_.Path -ieq $p }) { exit 1 }" >nul 2>nul
if errorlevel 1 goto tclashrunning
del /q "%data%\migration-running" 2>nul
copy /Y "reuse-b.txt" "%data%\use-legacy-tracker" >nul
if errorlevel 1 goto failed
if not exist "%data%\use-legacy-tracker" goto failed
"%collector%" -verify-migration >nul 2>nul
if errorlevel 1 goto invalidmigration
rem Stop only the separately installed old collector after the new app is present.
powershell -NoProfile -NonInteractive -Command "$p=Join-Path $env:LOCALAPPDATA 'Programs\Codex Usage Tracker\Codex Usage.exe'; Get-Process -Name 'Codex Usage' -ErrorAction SilentlyContinue | Where-Object { $_.Path -ieq $p } | Stop-Process -Force -ErrorAction Stop" >nul 2>nul
if errorlevel 1 goto migrationfailed
start "" "%target%"
set /a attempts=0
:waitformigration
if exist "%data%\migration-running" goto migrationready
set /a attempts+=1
if %attempts% GEQ 45 goto migrationfailed
timeout /t 1 /nobreak >nul
goto waitformigration
:migrationready
rem Keep a recoverable copy of the old startup command before disabling it.
powershell -NoProfile -NonInteractive -Command "$ErrorActionPreference='Stop'; $k='HKCU:\Software\Microsoft\Windows\CurrentVersion\Run'; $v=(Get-ItemProperty -LiteralPath $k -Name 'Codex Usage Tracker' -ErrorAction SilentlyContinue).'Codex Usage Tracker'; $p=Join-Path $env:LOCALAPPDATA 'Programs\Codex Usage Tracker\Codex Usage.exe'; if ($v) { if ($v.IndexOf($p, [System.StringComparison]::OrdinalIgnoreCase) -lt 0) { throw 'Unrecognized legacy startup command' }; Set-Content -LiteralPath (Join-Path $env:APPDATA 'TClash Preview\collector\legacy-autostart.txt') -Value $v -Encoding UTF8; Remove-ItemProperty -LiteralPath $k -Name 'Codex Usage Tracker' -ErrorAction Stop }" >nul 2>nul
if errorlevel 1 goto startupwarning
exit /b 0
:startupwarning
echo TClash started, but the old collector startup entry could not be disabled.
pause
exit /b 1
:startupblocked
echo The old collector startup entry could not be safely disabled. No new enrollment was installed.
pause
exit /b 1
:migrationfailed
del /q "%data%\use-legacy-tracker" 2>nul
start "" "%legacy%" -data-dir "%APPDATA%\Codex Usage Tracker" -background
echo Migration could not be confirmed. A restart of the old collector was requested.
pause
exit /b 1
:tclashrunning
echo Please exit TClash Preview before running this installer.
pause
exit /b 1
:oldrunning
echo Please exit the old Codex Usage Tracker before installing the new device.
pause
exit /b 1
:olddata
echo Old B collector data exists. Follow the included instructions to clear only the old B data, then run this installer again.
pause
exit /b 1
:wrongversion
echo The installed TClash collector is not the new version. No new enrollment was installed.
pause
exit /b 1
:alreadyconnected
echo TClash is using an existing device identity. New enrollment was not installed.
pause
exit /b 1
:invalidmigration
del /q "%data%\use-legacy-tracker" 2>nul
:nolegacy
echo The paired standalone collector was not found on this Windows device.
pause
exit /b 1
:failed
echo Could not install TClash Preview. Check the installation and retry.
pause
exit /b 1
