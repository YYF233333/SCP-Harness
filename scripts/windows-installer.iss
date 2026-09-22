#ifndef AppVersion
  #error AppVersion is required
#endif
#ifndef ReleaseDir
  #error ReleaseDir is required
#endif
#ifndef OutputDir
  #error OutputDir is required
#endif

[Setup]
AppId={{58716885-9559-4C12-B724-EE07D55B18DD}
AppName=SCP Harness
AppVersion={#AppVersion}
AppPublisher=SCP Harness
DefaultDirName={userpf}\SCP Harness
PrivilegesRequired=lowest
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
MinVersion=10.0.22000
DisableProgramGroupPage=yes
ChangesEnvironment=yes
CloseApplications=no
RestartApplications=no
OutputDir={#OutputDir}
OutputBaseFilename=SCP-Harness-{#AppVersion}-windows-x64-setup
Compression=lzma2
SolidCompression=yes
WizardStyle=modern
LicenseFile=..\LICENSE
UninstallDisplayName=SCP Harness {#AppVersion}

[Files]
Source: "{#ReleaseDir}\scp.exe"; DestDir: "{app}"; DestName: "scph.exe"; Flags: ignoreversion
Source: "..\LICENSE"; DestDir: "{app}"
Source: "..\docs\windows-installation.md"; DestDir: "{app}"; DestName: "README.md"; Flags: ignoreversion
Source: "..\scp.example.json"; DestDir: "{app}\examples"; Flags: ignoreversion
Source: "..\testdata\cards\*.json"; DestDir: "{app}\examples\testdata\cards"; Flags: ignoreversion

[Registry]
Root: HKCU; Subkey: "Environment"; ValueType: expandsz; ValueName: "SCP_CONFIG"; ValueData: "{localappdata}\SCP Harness\scp.json"; Flags: createvalueifdoesntexist

[Code]
function AppPathEntry(const Entry: String): Boolean;
begin
  Result := CompareText(RemoveBackslashUnlessRoot(Trim(Entry)),
    RemoveBackslashUnlessRoot(ExpandConstant('{app}'))) = 0;
end;

procedure UpdateUserPath(const Installing: Boolean);
var
  CurrentPath, Remaining, Entry, Updated: String;
  Separator: Integer;
  Found: Boolean;
begin
  RegQueryStringValue(HKCU, 'Environment', 'Path', CurrentPath);
  Remaining := CurrentPath;
  Updated := '';
  Found := False;
  while Remaining <> '' do
  begin
    Separator := Pos(';', Remaining);
    if Separator = 0 then
      Separator := Length(Remaining) + 1;
    Entry := Copy(Remaining, 1, Separator - 1);
    Delete(Remaining, 1, Separator);
    if AppPathEntry(Entry) then
      Found := True
    else
    begin
      if Updated <> '' then
        Updated := Updated + ';';
      Updated := Updated + Entry;
    end;
  end;
  if Installing then
  begin
    if Found then
      exit;
    if CurrentPath <> '' then
      Updated := CurrentPath + ';'
    else
      Updated := '';
    Updated := Updated + ExpandConstant('{app}');
  end
  else if not Found then
    exit;
  if not RegWriteExpandStringValue(HKCU, 'Environment', 'Path', Updated) then
    RaiseException('Could not update the user PATH.');
end;

procedure CurStepChanged(CurStep: TSetupStep);
begin
  if CurStep = ssPostInstall then
    UpdateUserPath(True);
end;

procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
var
  ConfigPath: String;
begin
  if CurUninstallStep = usPostUninstall then
  begin
    UpdateUserPath(False);
    if RegQueryStringValue(HKCU, 'Environment', 'SCP_CONFIG', ConfigPath) and
      (CompareText(ConfigPath, ExpandConstant('{localappdata}\SCP Harness\scp.json')) = 0) then
      RegDeleteValue(HKCU, 'Environment', 'SCP_CONFIG');
  end;
end;
