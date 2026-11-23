[Setup]
AppId={{APP_ID}}
AppVersion={{APP_VERSION}}
AppName={{DISPLAY_NAME}}
AppPublisher={{PUBLISHER_NAME}}
AppPublisherURL={{PUBLISHER_URL}}
AppSupportURL={{PUBLISHER_URL}}
AppUpdatesURL={{PUBLISHER_URL}}
DefaultDirName={code:PreferredTClashDir}
DisableProgramGroupPage=yes
OutputDir=.
OutputBaseFilename={{OUTPUT_BASE_FILENAME}}
Compression=lzma
SolidCompression=yes
SetupIconFile={{SETUP_ICON_FILE}}
WizardStyle=modern
PrivilegesRequired={{PRIVILEGES_REQUIRED}}
ArchitecturesAllowed={{ARCH}}
ArchitecturesInstallIn64BitMode={{ARCH}}

[Code]
const
  SYNCHRONIZE = $00100000;

function OpenProcess(dwDesiredAccess: DWORD; bInheritHandle: BOOL; dwProcessId: DWORD): THandle;
  external 'OpenProcess@kernel32.dll stdcall';
function WaitForSingleObject(hHandle: THandle; dwMilliseconds: DWORD): DWORD;
  external 'WaitForSingleObject@kernel32.dll stdcall';
function CloseHandle(hObject: THandle): BOOL;
  external 'CloseHandle@kernel32.dll stdcall';

// The app passes /updatefrom=<pid> (windowsUpdateArguments in lib/common)
// and exits on its own, stopping the Core and clearing the system proxy.
function UpdatingAppPid: Integer;
begin
  Result := StrToIntDef(ExpandConstant('{param:updatefrom|0}'), 0);
end;

function IsAppUpdate: Boolean;
begin
  Result := UpdatingAppPid > 0;
end;

function LegacyInstallDir(Root: Integer): String;
var
  Value: String;
  EndQuote: Integer;
begin
  Result := '';
  if not RegQueryStringValue(Root, 'Software\TClash Preview\TClash Preview', '', Value) then
    RegQueryStringValue(Root, 'Software\Microsoft\Windows\CurrentVersion\Uninstall\TClash Preview', 'InstallLocation', Value);
  Value := RemoveQuotes(Trim(Value));
  if (Value <> '') and FileExists(AddBackslash(Value) + 'tclash-preview.exe') then
  begin
    Result := Value;
    Exit;
  end;
  if RegQueryStringValue(Root, 'Software\Microsoft\Windows\CurrentVersion\Uninstall\TClash Preview', 'DisplayIcon', Value) then
  begin
    Value := Trim(Value);
    if (Length(Value) > 1) and (Value[1] = '"') then
    begin
      EndQuote := Pos('"', Copy(Value, 2, Length(Value)));
      if EndQuote > 0 then Value := Copy(Value, 2, EndQuote - 1);
    end else begin
      EndQuote := Pos(',', Value);
      if EndQuote > 0 then Value := Copy(Value, 1, EndQuote - 1);
    end;
    if (CompareText(ExtractFileName(Value), 'tclash-preview.exe') = 0) and FileExists(Value) then
      Result := ExtractFileDir(Value);
  end;
end;

function PreferredTClashDir(Param: String): String;
var
  OldPath: String;
begin
  OldPath := LegacyInstallDir(HKCU);
  if OldPath = '' then OldPath := LegacyInstallDir(HKCU32);
  if OldPath = '' then OldPath := LegacyInstallDir(HKLM);
  if OldPath = '' then OldPath := LegacyInstallDir(HKLM32);
  if OldPath = '' then
    Result := ExpandConstant('{autopf}\TClash')
  else
    Result := AddBackslash(ExtractFileDir(RemoveBackslash(OldPath))) + 'TClash';
end;

procedure WaitForUpdatingApp;
var
  Handle: THandle;
begin
  if not IsAppUpdate then
    Exit;
  Handle := OpenProcess(SYNCHRONIZE, False, UpdatingAppPid);
  if Handle <> 0 then
  begin
    WaitForSingleObject(Handle, 15000);
    CloseHandle(Handle);
  end;
end;

procedure KillProcesses;
var
  Processes: TArrayOfString;
  i: Integer;
  ResultCode: Integer;
begin
  Processes := ['TClash.exe', 'TClashCore.exe', 'TClashHelperService.exe'];

  for i := 0 to GetArrayLength(Processes)-1 do
  begin
    Exec('taskkill', '/f /im ' + Processes[i], '', SW_HIDE, ewWaitUntilTerminated, ResultCode);
  end;
end;

procedure UnregisterHelperService;
var
  HelperPath: String;
  ResultCode: Integer;
begin
  HelperPath := ExpandConstant('{app}\\TClashHelperService.exe');
  if FileExists(HelperPath) then
  begin
    Exec(HelperPath, 'uninstall', '', SW_HIDE, ewWaitUntilTerminated, ResultCode);
  end;
end;

procedure ClearStaleProxy;
var
  AppPath: String;
  ResultCode: Integer;
begin
  AppPath := ExpandConstant('{app}\\{{EXECUTABLE_NAME}}');
  if FileExists(AppPath) then
  begin
    Exec(AppPath, '--clear-stale-proxy', '', SW_HIDE, ewWaitUntilTerminated, ResultCode);
  end;
end;

function PointsIntoApp(Value: String): Boolean;
begin
  Result := Pos(Lowercase(AddBackslash(ExpandConstant('{app}'))), Lowercase(Value)) > 0;
end;

// The value name is appName and the schemes are protocolSchemes, both in
// lib/common.
procedure RemoveUserRegistrations;
var
  Schemes: TArrayOfString;
  Value: String;
  i: Integer;
begin
  if RegQueryStringValue(HKCU, 'Software\Microsoft\Windows\CurrentVersion\Run', '{{APP_NAME}}', Value) and PointsIntoApp(Value) then
  begin
    RegDeleteValue(HKCU, 'Software\Microsoft\Windows\CurrentVersion\Run', '{{APP_NAME}}');
    RegDeleteValue(HKCU, 'Software\Microsoft\Windows\CurrentVersion\Explorer\StartupApproved\Run', '{{APP_NAME}}');
  end;

  Schemes := ['clash', 'clashmeta', 'flclash'];
  for i := 0 to GetArrayLength(Schemes)-1 do
  begin
    if RegQueryStringValue(HKCU, 'Software\Classes\' + Schemes[i] + '\shell\open\command', '', Value) and PointsIntoApp(Value) then
    begin
      RegDeleteKeyIncludingSubkeys(HKCU, 'Software\Classes\' + Schemes[i]);
    end;
  end;
end;

function PrepareToInstall(var NeedsRestart: Boolean): String;
var
  Code: Integer;
  StopLegacy: String;
begin
  StopLegacy := '$session = (Get-Process -Id $PID).SessionId; ' +
    '$apps = @(Get-Process -Name tclash-preview -ErrorAction SilentlyContinue | ' +
    'Where-Object SessionId -eq $session); ' +
    'foreach ($app in $apps) { $null = $app.CloseMainWindow() }; ' +
    'foreach ($app in $apps) { if (!$app.WaitForExit(6000)) { exit 1 } }; exit 0';
  if not Exec(ExpandConstant('{sys}\WindowsPowerShell\v1.0\powershell.exe'),
    '-NoProfile -NonInteractive -Command "' + StopLegacy + '"', '', SW_HIDE,
    ewWaitUntilTerminated, Code) or (Code <> 0) then
  begin
    Result := CustomMessage('CloseLegacyApp');
    Exit;
  end;
  WaitForUpdatingApp;
  UnregisterHelperService;
  KillProcesses;
  Result := '';
end;

procedure CurStepChanged(CurStep: TSetupStep);
begin
  if CurStep = ssPostInstall then
  begin
    ClearStaleProxy;
  end;
end;

function InitializeUninstall(): Boolean;
begin
  UnregisterHelperService;
  KillProcesses;
  ClearStaleProxy;
  Result := True;
end;

// path_provider names these folders after CompanyName and ProductName in
// windows/runner/Runner.rc.
procedure RemoveUserData;
begin
  DelTree(ExpandConstant('{userappdata}\TClash'), True, True, True);
  DelTree(ExpandConstant('{localappdata}\TClash'), True, True, True);
end;

procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
begin
  if CurUninstallStep = usPostUninstall then
  begin
    RemoveUserRegistrations;
    if not UninstallSilent and (MsgBox(CustomMessage('RemoveUserData'), mbConfirmation, MB_YESNO or MB_DEFBUTTON2) = IDYES) then
    begin
      RemoveUserData;
    end;
  end;
end;

[Languages]
{% for locale in LOCALES %}
{% if locale.lang == 'en' %}Name: "english"; MessagesFile: "compiler:Default.isl"{% endif %}
{% if locale.lang == 'hy' %}Name: "armenian"; MessagesFile: "compiler:Languages\\Armenian.isl"{% endif %}
{% if locale.lang == 'bg' %}Name: "bulgarian"; MessagesFile: "compiler:Languages\\Bulgarian.isl"{% endif %}
{% if locale.lang == 'ca' %}Name: "catalan"; MessagesFile: "compiler:Languages\\Catalan.isl"{% endif %}
{% if locale.lang == 'zh' %}
Name: "chineseSimplified"; MessagesFile: {% if locale.file %}{{ locale.file }}{% else %}"compiler:Languages\\ChineseSimplified.isl"{% endif %}
{% endif %}
{% if locale.lang == 'co' %}Name: "corsican"; MessagesFile: "compiler:Languages\\Corsican.isl"{% endif %}
{% if locale.lang == 'cs' %}Name: "czech"; MessagesFile: "compiler:Languages\\Czech.isl"{% endif %}
{% if locale.lang == 'da' %}Name: "danish"; MessagesFile: "compiler:Languages\\Danish.isl"{% endif %}
{% if locale.lang == 'nl' %}Name: "dutch"; MessagesFile: "compiler:Languages\\Dutch.isl"{% endif %}
{% if locale.lang == 'fi' %}Name: "finnish"; MessagesFile: "compiler:Languages\\Finnish.isl"{% endif %}
{% if locale.lang == 'fr' %}Name: "french"; MessagesFile: "compiler:Languages\\French.isl"{% endif %}
{% if locale.lang == 'de' %}Name: "german"; MessagesFile: "compiler:Languages\\German.isl"{% endif %}
{% if locale.lang == 'he' %}Name: "hebrew"; MessagesFile: "compiler:Languages\\Hebrew.isl"{% endif %}
{% if locale.lang == 'is' %}Name: "icelandic"; MessagesFile: "compiler:Languages\\Icelandic.isl"{% endif %}
{% if locale.lang == 'it' %}Name: "italian"; MessagesFile: "compiler:Languages\\Italian.isl"{% endif %}
{% if locale.lang == 'ja' %}Name: "japanese"; MessagesFile: "compiler:Languages\\Japanese.isl"{% endif %}
{% if locale.lang == 'no' %}Name: "norwegian"; MessagesFile: "compiler:Languages\\Norwegian.isl"{% endif %}
{% if locale.lang == 'pl' %}Name: "polish"; MessagesFile: "compiler:Languages\\Polish.isl"{% endif %}
{% if locale.lang == 'pt' %}Name: "portuguese"; MessagesFile: "compiler:Languages\\Portuguese.isl"{% endif %}
{% if locale.lang == 'ru' %}Name: "russian"; MessagesFile: "compiler:Languages\\Russian.isl"{% endif %}
{% if locale.lang == 'sk' %}Name: "slovak"; MessagesFile: "compiler:Languages\\Slovak.isl"{% endif %}
{% if locale.lang == 'sl' %}Name: "slovenian"; MessagesFile: "compiler:Languages\\Slovenian.isl"{% endif %}
{% if locale.lang == 'es' %}Name: "spanish"; MessagesFile: "compiler:Languages\\Spanish.isl"{% endif %}
{% if locale.lang == 'tr' %}Name: "turkish"; MessagesFile: "compiler:Languages\\Turkish.isl"{% endif %}
{% if locale.lang == 'uk' %}Name: "ukrainian"; MessagesFile: "compiler:Languages\\Ukrainian.isl"{% endif %}
{% endfor %}

[CustomMessages]
english.RemoveUserData=Also delete your profiles and settings?
english.CloseLegacyApp=Exit TClash Preview from its tray menu, then continue setup.
chineseSimplified.CloseLegacyApp=请从托盘菜单退出 TClash Preview，然后继续安装。

[Tasks]
Name: "desktopicon"; Description: "{cm:CreateDesktopIcon}"; GroupDescription: "{cm:AdditionalIcons}"; Flags: {% if CREATE_DESKTOP_ICON != true %}unchecked{% else %}checkedonce{% endif %}
[Files]
Source: "{{SOURCE_DIR}}\\*"; DestDir: "{app}"; Flags: ignoreversion recursesubdirs createallsubdirs
; NOTE: Don't use "Flags: ignoreversion" on any shared system files

[Icons]
Name: "{autoprograms}\\{{DISPLAY_NAME}}"; Filename: "{app}\\{{EXECUTABLE_NAME}}"; WorkingDir: "{app}"
Name: "{autodesktop}\\{{DISPLAY_NAME}}"; Filename: "{app}\\{{EXECUTABLE_NAME}}"; WorkingDir: "{app}"; Tasks: desktopicon
[Run]
Filename: "{app}\\{{EXECUTABLE_NAME}}"; WorkingDir: "{app}"; Description: "{cm:LaunchProgram,{{DISPLAY_NAME}}}"; Flags: runasoriginaluser nowait postinstall skipifsilent
Filename: "{app}\\{{EXECUTABLE_NAME}}"; WorkingDir: "{app}"; Flags: runasoriginaluser nowait; Check: IsAppUpdate
