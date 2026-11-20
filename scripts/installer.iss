; MCPProxy Windows Installer - Inno Setup Script
; This installer packages both mcpproxy.exe (core server) and mcpproxy-tray.exe (GUI application)
; for Windows 10 version 21H2+ and Windows 11 (amd64/arm64)

#define AppName "MCPProxy"
#define AppPublisher "Smart MCP Proxy"
#define AppURL "https://github.com/smart-mcp-proxy/mcpproxy-go"
#define AppExeName "mcpproxy-tray.exe"
#define UpgradeCode "{{A1B2C3D4-E5F6-7890-ABCD-EF1234567890}}"

; Refuse to build with an Inno Setup compiler that leaks the RedirectionGuard
; process mitigation into [Run] postinstall children (issue #1844). 6.7.0-6.7.3
; are affected; 6.7.4 fixes the inheritance. CI installs a pinned 6.7.4 via
; .github/actions/install-inno-setup. (Ver is the preprocessor version, which
; tracks the Inno Setup release.) Not verified on macOS - ISCC is Windows-only.
#if Ver < EncodeVer(6,7,4)
  #error "Inno Setup 6.7.4 or newer is required (6.7.0-6.7.3 leak RedirectionGuard into postinstall children, issue #1844)"
#endif

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
OutputDir=..\dist
OutputBaseFilename=mcpproxy-setup-{#Version}-{#Arch}
Compression=lzma2
SolidCompression=yes

; Architecture support (multi-arch single installer)
ArchitecturesAllowed=x64compatible arm64
ArchitecturesInstallIn64BitMode=x64compatible arm64

; System requirements
MinVersion=10.0.19044
PrivilegesRequired=admin
ChangesEnvironment=yes

; UI configuration
WizardStyle=modern
DisableWelcomePage=no
LicenseFile=..\LICENSE
; InfoBeforeFile will be added in Phase 6 (US4)
; InfoAfterFile will be added in Phase 6 (US4)

; Uninstall configuration
UninstallDisplayIcon={app}\{#AppExeName}
UninstallDisplayName={#AppName}

[Languages]
Name: "english"; MessagesFile: "compiler:Default.isl"

[Files]
; Core binary (architecture-specific)
Source: "{#BinPath}\mcpproxy.exe"; DestDir: "{app}"; Flags: ignoreversion

; Tray application (architecture-specific)
Source: "{#BinPath}\mcpproxy-tray.exe"; DestDir: "{app}"; Flags: ignoreversion

; Release notes (optional - included if available, installed to docs subdirectory)
#ifdef ReleaseNotesPath
Source: "{#ReleaseNotesPath}"; DestDir: "{app}\docs"; DestName: "RELEASE_NOTES.md"; Flags: ignoreversion
#endif

[Icons]
; Start Menu shortcut (FR-004)
Name: "{group}\{#AppName}"; Filename: "{app}\{#AppExeName}"; WorkingDir: "{app}"

; Desktop shortcut disabled per FR-004 (Start Menu only)
; Name: "{autodesktop}\{#AppName}"; Filename: "{app}\{#AppExeName}"; Tasks: desktopicon

[Registry]
; System PATH modification (FR-003, US2)
; Add installation directory to system-level PATH for all users
Root: HKLM; Subkey: "SYSTEM\CurrentControlSet\Control\Session Manager\Environment"; \
    ValueType: expandsz; ValueName: "Path"; ValueData: "{olddata};{app}"; \
    Check: NeedsAddPath('{app}')

; Application metadata for tracking
Root: HKCU; Subkey: "SOFTWARE\{#AppPublisher}\{#AppName}"; \
    ValueType: string; ValueName: "InstallDate"; ValueData: "{code:GetInstallDateString}"; \
    Flags: uninsdeletekey

Root: HKCU; Subkey: "SOFTWARE\{#AppPublisher}\{#AppName}"; \
    ValueType: string; ValueName: "Architecture"; ValueData: "{#Arch}"

; RedirectionGuard (issue #1844): Inno Setup 6.7.0+ enables this Windows process
; mitigation on the Setup process by default, and we keep that default. In
; 6.7.0-6.7.3 a "postinstall" entry below inherited it, so the tray, the core and
; every upstream child started from "Launch MCPProxy" ran with
; EnforceRedirectionTrust=1 and uv/uvx servers failed with "untrusted mount point
; (os error 448)" (uv's Python links are junctions made by a standard user).
; Inno Setup 6.7.4 stops the inheritance, hence the Ver check at the top of this
; file. Residual caveat from the 6.7.4 release notes: if Setup did not have to
; request elevation (for example it was started from an already elevated shell)
; the mitigation is still inherited by the launched app. Workaround: quit
; MCPProxy and start it from the Start menu. The core logs a WARN and
; `mcpproxy doctor` reports it when the mitigation is active.
[Run]
; Post-install launch option (FR-005, US3)
; Launch tray application after installation completes (optional checkbox)
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

function GetInstallDateString(Param: string): string;
begin
  Result := GetDateTimeString('yyyy-mm-dd hh:nn:ss', '-', ':');
end;

// PATH manipulation functions (US2 - FR-003)
function NeedsAddPath(Param: string): Boolean;
var
  OrigPath: string;
begin
  // Query current system PATH
  if not RegQueryStringValue(HKEY_LOCAL_MACHINE,
    'SYSTEM\CurrentControlSet\Control\Session Manager\Environment',
    'Path', OrigPath)
  then begin
    Result := True;
    exit;
  end;

  // Check if installation directory already in PATH (case-insensitive)
  // Add semicolons to ensure exact directory match (not substring)
  Result := Pos(';' + Uppercase(Param) + ';', ';' + Uppercase(OrigPath) + ';') = 0;

  // PATH length validation (FR-015) - Windows limit is 2047 characters
  if Result and (Length(OrigPath) + Length(Param) + 1 > 2047) then
  begin
    MsgBox('Warning: Adding MCPProxy to PATH would exceed Windows limit (2047 characters).' + #13#10 +
           'You may need to manually clean up your PATH environment variable.', mbError, MB_OK);
    Result := False;
  end;
end;

// Process detection (optional - check if mcpproxy apps are running)
function InitializeSetup(): Boolean;
begin
  Result := True;
  // Future enhancement: Check for running processes
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
    // No action needed - this is the default behavior per FR-019
  end;
end;
