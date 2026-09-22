# SCP Harness for Windows

The global command is `scph`; Windows OpenSSH's `scp` remains available.

Run `SCP-Harness-<version>-windows-x64-setup.exe` to install for the current user.
The default program directory is `%LOCALAPPDATA%\Programs\SCP Harness`. Setup
adds that directory to the user PATH and registers an entry in Windows Installed
apps. Open a new terminal after installation:

```powershell
scph --version
scph --json status
```

## Configuration and data

Setup sets `SCP_CONFIG` to `%LOCALAPPDATA%\SCP Harness\scp.json` if the environment
variable is not already configured. `--config PATH` overrides it. Without either,
the CLI reads `scp.json` from the current directory, as before. `--version` needs
no configuration or database; `--json --version` returns the standard envelope.

The installer contains the controller and example configuration/cards. It does
not contain credentials, existing tasks, WSL distributions or an authenticated
worker. Windows 11 x64, Git, Python and the provisioned `SCP-Worker`/`SCP-Test`
WSL2 environments are required for execution. Use the repository's operations
guide for provisioning.

For a new deployment, copy `examples\scp.example.json` and its `testdata` directory
from the installation to your data directory, then explicitly configure worker
commands, role cards, database/artifact paths and limits before `scph init`.
For an existing deployment, preserve its database and Artifact store together;
existing path references must remain valid. Keep mutable data outside the program
directory. Upgrades preserve `SCP_CONFIG` and data; uninstall removes the program
and its PATH entry but retains your configuration and task history.

## Building an installer

`cmd/scp/VERSION` is the version source embedded in every build. Commit the source,
run the daily Linux tests and the Windows release acceptance, then package the
exact accepted binary with [Inno Setup 6](https://jrsoftware.org/isdl.php):

```powershell
.\scripts\test-daily.ps1
.\scripts\final-acceptance.ps1 -KnownNormativeFailures none
.\scripts\package-windows.ps1 -Compiler 'C:\path\to\ISCC.exe'
```

The installer, SHA-256 and source/binary manifest go to `.local\releases\v<version>`.
The installer renames the accepted `scp.exe` to `scph.exe` without rebuilding it.
Packages built locally are unsigned.
