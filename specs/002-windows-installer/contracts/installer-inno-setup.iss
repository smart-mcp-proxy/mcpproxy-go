; MCPProxy Windows Installer - Inno Setup Script
; This is a CONTRACT/SCHEMA file showing the installer structure
; Actual implementation will be in scripts/installer.iss

#define AppName "MCPProxy"
#define AppPublisher "Smart MCP Proxy"
#define AppURL "https://github.com/smart-mcp-proxy/mcpproxy-go"
#define AppExeName "mcpproxy-tray.exe"
#define UpgradeCode "{{A1B2C3D4-E5F6-7890-ABCD-EF1234567890}"

; Build variables (passed via command line)
#ifndef Version
  #define Version "1.0.0"
#endif
#ifndef Arch
  #define Arch "amd64"
#endif
#ifndef BinPath
  #define BinPath "dist\windows-" + Arch
#endif

[Setup]
; Identification
AppId={#UpgradeCode}
AppName={#AppName}
AppVersion={#Version}
AppPublisher={#AppPublisher}
AppPublisherURL={#AppURL}
AppSupportURL={#AppURL}
AppUpdatesURL={#AppURL}/releases

; Installation directories
DefaultDirName={autopf}\{#AppName}
DefaultGroupName={#AppName}
DisableProgramGroupPage=yes

; Output configuration
OutputDir=dist
OutputBaseFilename=mcpproxy-setup-{#Version}-{#Arch}
Compression=lzma2
SolidCompression=yes

; Architecture support (multi-arch single installer)
ArchitecturesAllowed=x64compatible arm64compatible
ArchitecturesInstallIn64BitMode=x64compatible arm64compatible

; System requirements
MinVersion=10.0.19044
PrivilegesRequired=admin
ChangesEnvironment=yes

; UI configuration
WizardStyle=modern
DisableWelcomePage=no
LicenseFile=scripts\installer-resources\windows\license.txt
InfoBeforeFile=scripts\installer-resources\windows\welcome.rtf
InfoAfterFile=scripts\installer-resources\windows\conclusion.rtf

; Uninstall configuration
UninstallDisplayIcon={app}\{#AppExeName}
UninstallDisplayName={#AppName}

[Languages]
Name: "english"; MessagesFile: "compiler:Default.isl"

[Files]
; Core binary (architecture-specific)
Source: "{#BinPath}\mcpproxy.exe"; DestDir: "{app}"; Flags: ignoreversion; Check: IsCurrentArch

; Tray application (architecture-specific)
Source: "{#BinPath}\mcpproxy-tray.exe"; DestDir: "{app}"; Flags: ignoreversion; Check: IsCurrentArch

[Icons]
; Start Menu shortcut
Name: "{group}\{#AppName}"; Filename: "{app}\{#AppExeName}"; WorkingDir: "{app}"

; Desktop shortcut (optional, commented out - Start Menu only per FR-004)
; Name: "{autodesktop}\{#AppName}"; Filename: "{app}\{#AppExeName}"; Tasks: desktopicon

[Registry]
; Add to system PATH
Root: HKLM; Subkey: "SYSTEM\CurrentControlSet\Control\Session Manager\Environment"; \
    ValueType: expandsz; ValueName: "Path"; ValueData: "{olddata};{app}"; \
    Check: NeedsAddPath('{app}')

; Application metadata (optional analytics)
Root: HKCU; Subkey: "SOFTWARE\{#AppPublisher}\{#AppName}"; \
    ValueType: string; ValueName: "InstallDate"; ValueData: "{code:GetDateTimeString}"; \
    Flags: uninsdeletekey

Root: HKCU; Subkey: "SOFTWARE\{#AppPublisher}\{#AppName}"; \
    ValueType: string; ValueName: "Architecture"; ValueData: "{#Arch}"

[Run]
; Post-install: Optional launch of tray application
Filename: "{app}\{#AppExeName}"; Description: "{cm:LaunchProgram,{#StringChange(AppName, '&', '&&')}}"; \
    Flags: nowait postinstall skipifsilent

[Code]
// Architecture detection functions
function IsX64: Boolean;
begin
  Result := ProcessorArchitecture = paX64;
end;

function IsARM64: Boolean;
begin
  Result := ProcessorArchitecture = paARM64;
end;

function IsCurrentArch: Boolean;
begin
  Result := (IsX64 and ('{#Arch}' = 'amd64')) or (IsARM64 and ('{#Arch}' = 'arm64'));
end;

// PATH manipulation functions
function NeedsAddPath(Param: string): Boolean;
var
  OrigPath: string;
begin
  if not RegQueryStringValue(HKEY_LOCAL_MACHINE,
    'SYSTEM\CurrentControlSet\Control\Session Manager\Environment',
    'Path', OrigPath)
  then begin
    Result := True;
    exit;
  end;
  // Check if already in PATH (case-insensitive)
  Result := Pos(';' + Uppercase(Param) + ';', ';' + Uppercase(OrigPath) + ';') = 0;
end;

function GetDateTimeString(Param: string): string;
begin
  Result := GetDateTimeString('yyyy-mm-dd hh:nn:ss', #0, #0);
end;

// Process detection (optional - check if mcpproxy apps are running)
function InitializeSetup(): Boolean;
begin
  Result := True;
  // TODO: Add process detection logic here
  // if IsAppRunning('mcpproxy.exe') or IsAppRunning('mcpproxy-tray.exe') then
  //   MsgBox('MCPProxy is currently running. Please close it before installing.', mbError, MB_OK);
  //   Result := False;
end;

// Custom uninstall preservation of user data
procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
begin
  if CurUninstallStep = usPostUninstall then
  begin
    // User data in %USERPROFILE%\.mcpproxy is automatically preserved
    // No action needed - this is the default behavior
  end;
end;
