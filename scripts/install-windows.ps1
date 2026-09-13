param(
    [string]$BinaryPath = "",
    [string]$InstallRootOverride = ""
)

$ErrorActionPreference = "Stop"

$InstallRoot = if ($InstallRootOverride) {
    $InstallRootOverride
} else {
    Join-Path $env:LOCALAPPDATA "SSHDESK"
}
$BinDir = Join-Path $InstallRoot "bin"
$Binary = Join-Path $BinDir "sshdesk.exe"

New-Item -ItemType Directory -Force -Path $InstallRoot, $BinDir | Out-Null
if ($BinaryPath) {
    Copy-Item $BinaryPath $Binary -Force
} elseif (-not (Test-Path $Binary)) {
    # Repository-local use: a binary built with `go build -o sshdesk.exe
    # ./cmd/sshdesk` at the checkout root.
    $LocalBinary = Join-Path (Split-Path -Parent $PSScriptRoot) "sshdesk.exe"
    if (Test-Path $LocalBinary) {
        Copy-Item $LocalBinary $Binary -Force
    } else {
        throw "no SSHDESK binary found; pass -BinaryPath or run scripts/install.ps1"
    }
}

# The Go build is a single binary; each command name is a .cmd wrapper that
# forwards to its subcommand.
$Wrappers = @(
    @("sshdesk-server", "server"),
    @("sshdesk-local", "local"),
    @("sshdesk-bench", "bench"),
    @("sshdesk-forced-command", "forced-command"),
    @("sshdesk-agent", "agent"),
    @("sshdesk-agent-ssh", "agent-ssh"),
    @("sshdesk-remote", "remote"),
    @("sshdesk-split", "split")
)
foreach ($Entry in $Wrappers) {
    $Wrapper = Join-Path $BinDir "$($Entry[0]).cmd"
    Set-Content -Encoding Ascii -Path $Wrapper -Value "@`"$Binary`" $($Entry[1]) %*"
}
Set-Content -Encoding Ascii -Path (Join-Path $BinDir "sshdesk.cmd") `
    -Value "@`"$Binary`" %*"

$UserPath = [Environment]::GetEnvironmentVariable("Path", "User")
if (($UserPath -split ";") -notcontains $BinDir) {
    $NewPath = if ($UserPath) { "$UserPath;$BinDir" } else { $BinDir }
    [Environment]::SetEnvironmentVariable("Path", $NewPath, "User")
}
$env:Path = "$BinDir;$env:Path"

Write-Host "Installed SSHDESK in $InstallRoot."
Write-Host "Add $BinDir to PATH. Windows hosting is experimental and must run"
Write-Host "inside the logged-in interactive desktop session."
