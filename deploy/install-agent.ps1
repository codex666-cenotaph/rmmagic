<#
.SYNOPSIS
    Install and enroll the rmmagic endpoint agent on Windows.

.DESCRIPTION
    Downloads (or uses a provided) rmmagent.exe, enrolls the device with the
    rmmagic platform, installs it as a Windows service, and starts it.

    Must be run as Administrator.

.PARAMETER Server
    Server base URL, e.g. https://rmm.example.com

.PARAMETER Token
    Enrollment token (rmme_...). Prefer the RMM_ENROLL_TOKEN environment
    variable to avoid the token appearing in shell history.

.PARAMETER StateDir
    Directory for the device identity and command journal.
    Defaults to %ProgramData%\rmmagent.

.PARAMETER Bin
    Path to a pre-built rmmagent.exe binary. If omitted the script downloads
    the latest release from the server.

.PARAMETER InstallDir
    Directory where rmmagent.exe is installed.
    Defaults to %ProgramFiles%\rmmagic.

.PARAMETER SkipEnroll
    Skip enrollment (device already enrolled; just install the service).

.PARAMETER Service
    Install and start the service without prompting.

.PARAMETER NoService
    Skip service installation (binary and enrollment only).

.PARAMETER Yes
    Assume yes for all interactive prompts.

.EXAMPLE
    # Fully automated deployment (token from environment):
    $env:RMM_ENROLL_TOKEN = "rmme_abc123"
    .\install-agent.ps1 -Server https://rmm.example.com -Service -Yes

.EXAMPLE
    # Use a pre-downloaded binary:
    .\install-agent.ps1 -Server https://rmm.example.com -Token rmme_abc123 `
        -Bin .\rmmagent.exe -Service
#>
#Requires -RunAsAdministrator
[CmdletBinding()]
param(
    [string]$Server       = $env:RMM_SERVER,
    [string]$Token        = $env:RMM_ENROLL_TOKEN,
    [string]$StateDir     = $(if ($env:RMM_STATE_DIR) { $env:RMM_STATE_DIR } else { Join-Path $env:ProgramData 'rmmagent' }),
    [string]$Bin          = $env:RMM_AGENT_BIN,
    [string]$InstallDir   = $(if ($env:RMM_INSTALL_DIR) { $env:RMM_INSTALL_DIR } else { Join-Path $env:ProgramFiles 'rmmagic' }),
    [switch]$SkipEnroll,
    [switch]$Service,
    [switch]$NoService,
    [switch]$Yes
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

function Write-Step([string]$msg) { Write-Host "==> $msg" -ForegroundColor Cyan }
function Write-Ok([string]$msg)   { Write-Host "    $msg" -ForegroundColor Green }
function Write-Warn([string]$msg) { Write-Host "    WARNING: $msg" -ForegroundColor Yellow }

function Confirm-Step([string]$prompt) {
    if ($Yes -or $Service) { return $true }
    $ans = Read-Host "$prompt [Y/n]"
    return ($ans -eq '' -or $ans -match '^[Yy]')
}

# ---------------------------------------------------------------------------
# 1. Resolve binary
# ---------------------------------------------------------------------------
Write-Step "Resolving agent binary"

$agentExe = Join-Path $InstallDir 'rmmagent.exe'

if ($Bin) {
    if (-not (Test-Path $Bin)) {
        Write-Error "Binary not found: $Bin"
        exit 1
    }
    Write-Ok "Using provided binary: $Bin"
} else {
    # Download from server's release endpoint.
    if (-not $Server) {
        Write-Error "Provide -Server or set RMM_SERVER to download the binary."
        exit 1
    }
    $downloadUrl = $Server.TrimEnd('/') + '/releases/latest/rmmagent-windows-amd64.exe'
    $tmpBin = Join-Path $env:TEMP 'rmmagent.exe'
    Write-Ok "Downloading $downloadUrl"
    Invoke-WebRequest -Uri $downloadUrl -OutFile $tmpBin -UseBasicParsing
    $Bin = $tmpBin
}

# ---------------------------------------------------------------------------
# 2. Install binary
# ---------------------------------------------------------------------------
Write-Step "Installing binary to $InstallDir"

if (-not (Test-Path $InstallDir)) {
    New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
}
Copy-Item -Path $Bin -Destination $agentExe -Force
Write-Ok "Installed: $agentExe"

# ---------------------------------------------------------------------------
# 3. Enroll
# ---------------------------------------------------------------------------
if (-not $SkipEnroll) {
    Write-Step "Enrolling device"

    if (-not $Server) {
        $Server = Read-Host "Server URL (e.g. https://rmm.example.com)"
    }
    if (-not $Token) {
        $Token = Read-Host "Enrollment token (rmme_...)"
    }

    & $agentExe enroll --server $Server --token $Token --state-dir $StateDir
    if ($LASTEXITCODE -ne 0) {
        Write-Error "Enrollment failed (exit $LASTEXITCODE)"
        exit 1
    }
    Write-Ok "Device enrolled; identity stored in $StateDir"
} else {
    Write-Ok "Skipping enrollment (--SkipEnroll)"
}

# ---------------------------------------------------------------------------
# 4. Windows service
# ---------------------------------------------------------------------------
if ($NoService) {
    Write-Ok "Skipping service installation (--NoService)"
    exit 0
}

$installSvc = $Service -or (Confirm-Step "Install and start the rmmagent Windows service?")
if (-not $installSvc) {
    Write-Ok "Skipping service installation."
    exit 0
}

Write-Step "Installing Windows service"

# Remove stale service if present (e.g. re-run of this script).
$existingSvc = Get-Service -Name 'rmmagent' -ErrorAction SilentlyContinue
if ($existingSvc) {
    Write-Warn "Service already exists — removing and reinstalling."
    & $agentExe uninstall-service
    if ($LASTEXITCODE -ne 0) {
        Write-Error "Failed to remove existing service (exit $LASTEXITCODE)"
        exit 1
    }
}

& $agentExe install-service --state-dir $StateDir
if ($LASTEXITCODE -ne 0) {
    Write-Error "Service installation failed (exit $LASTEXITCODE)"
    exit 1
}
Write-Ok "Service registered (rmmagent, auto-start)"

Write-Step "Starting service"
Start-Service -Name 'rmmagent'
Write-Ok "Service started"

Write-Host ""
Write-Host "rmmagic endpoint agent is installed and running." -ForegroundColor Green
Write-Host "Device identity: $StateDir"
Write-Host "To check status: Get-Service rmmagent"
Write-Host "To view logs:    Get-EventLog -LogName Application -Source rmmagent -Newest 20"
