[CmdletBinding()]
param(
    [string] $UninstallerPath = (Join-Path $PSScriptRoot '..\install\uninstall-windows.ps1')
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$tokens = $null
$parseErrors = $null
$uninstallerAst = [Management.Automation.Language.Parser]::ParseFile(
    (Resolve-Path -LiteralPath $UninstallerPath).Path,
    [ref] $tokens,
    [ref] $parseErrors
)
if ($parseErrors.Count -ne 0) {
    throw "$UninstallerPath contains PowerShell syntax errors: $($parseErrors.Message -join '; ')"
}

# Execute the production functions and shutdown calls without running uninstall
# side effects. Only process enumeration/termination and sleeping are mocked.
foreach ($name in @('Get-ProcessIdsByPath', 'Stop-ProcessByPath')) {
    $definition = $uninstallerAst.Find({
        param($node)
        $node -is [Management.Automation.Language.FunctionDefinitionAst] -and $node.Name -eq $name
    }, $true)
    if ($null -eq $definition) {
        throw "$UninstallerPath does not define $name"
    }
    . ([scriptblock]::Create($definition.Extent.Text))
}
$shutdownStatements = @($uninstallerAst.EndBlock.Statements | Where-Object {
    $_.Extent.Text.StartsWith('Stop-ProcessByPath ')
})
if ($shutdownStatements.Count -ne 3) {
    throw 'Expected the uninstaller to stop the tray, Core/supervisor, and cloudflared.'
}

$testRoot = Join-Path ([IO.Path]::GetTempPath()) ('agentdock-uninstall-test-' + [Guid]::NewGuid().ToString('N'))
$installDir = Join-Path $testRoot 'AgentDock bin'
$otherInstallDir = Join-Path $testRoot 'Other AgentDock bin'
$trayBinary = Join-Path $installDir 'agentdock-tray.exe'
$agentDockBinary = Join-Path $installDir 'agentdock.exe'
$cloudflaredBinary = Join-Path $installDir 'cloudflared.exe'
$script:processes = @{}
$script:stoppedIds = @()
$script:sleepCount = 0
$script:cimUnavailable = $false

function Add-FakeProcess {
    param([int] $ProcessId, [string] $BinaryPath)
    $script:processes[$ProcessId] = [pscustomobject] @{
        ProcessId = $ProcessId
        Id = $ProcessId
        ExecutablePath = $BinaryPath
        Path = $BinaryPath
        Name = [IO.Path]::GetFileName($BinaryPath)
    }
}

function Get-CimInstance {
    [CmdletBinding()]
    param([string] $ClassName, [string] $Filter)
    if ($script:cimUnavailable) {
        throw 'Fixture: CIM unavailable'
    }
    if ($ClassName -ne 'Win32_Process' -or $Filter -notmatch "^Name = '([^']+)'$") {
        throw "Unexpected process query: $ClassName / $Filter"
    }
    $name = $Matches[1]
    return @($script:processes.Values | Where-Object { $_.Name -eq $name })
}

function Get-Process {
    [CmdletBinding()]
    param([string] $Name)
    return @($script:processes.Values | Where-Object { $_.Name -eq "$Name.exe" })
}

function Stop-Process {
    [CmdletBinding()]
    param([int] $Id, [switch] $Force)
    if (-not $Force -or -not $script:processes.ContainsKey($Id)) {
        throw "Unexpected process termination: $Id"
    }
    if ($Id -ge 9000) {
        throw "Uninstaller attempted to stop another installation's process: $Id"
    }
    if ($Id -eq 104 -and $script:processes.ContainsKey(102)) {
        throw 'Uninstaller stopped cloudflared while the Tunnel supervisor could still restart Core.'
    }
    $script:stoppedIds += $Id
    $script:processes.Remove($Id)
    if ($Id -eq 101) {
        # Reproduce a Core started by the supervisor after process enumeration.
        Add-FakeProcess -ProcessId 103 -BinaryPath $agentDockBinary
    }
}

function Start-Sleep {
    param([int] $Milliseconds)
    $script:sleepCount++
    if ($script:sleepCount -gt 8) {
        throw 'Uninstaller waited without terminating the replacement Core process.'
    }
}

try {
    foreach ($directory in @($installDir, $otherInstallDir)) {
        New-Item -ItemType Directory -Path $directory -Force | Out-Null
        foreach ($binary in @('agentdock-tray.exe', 'agentdock.exe', 'cloudflared.exe')) {
            [IO.File]::WriteAllText((Join-Path $directory $binary), 'fixture')
        }
    }
    foreach ($fallback in @($false, $true)) {
        $script:cimUnavailable = $fallback
        foreach ($fullShutdown in @($false, $true)) {
            $script:processes = @{}
            $script:stoppedIds = @()
            $script:sleepCount = 0
            Add-FakeProcess -ProcessId 101 -BinaryPath $agentDockBinary
            Add-FakeProcess -ProcessId 102 -BinaryPath $agentDockBinary
            Add-FakeProcess -ProcessId 9001 -BinaryPath (Join-Path $otherInstallDir 'agentdock.exe')
            if ($fullShutdown) {
                Add-FakeProcess -ProcessId 100 -BinaryPath $trayBinary
                Add-FakeProcess -ProcessId 104 -BinaryPath $cloudflaredBinary
                Add-FakeProcess -ProcessId 9000 -BinaryPath (Join-Path $otherInstallDir 'agentdock-tray.exe')
                Add-FakeProcess -ProcessId 9004 -BinaryPath (Join-Path $otherInstallDir 'cloudflared.exe')
                foreach ($statement in $shutdownStatements) {
                    & ([scriptblock]::Create($statement.Extent.Text))
                }
            } else {
                Stop-ProcessByPath -ProcessName 'agentdock' -BinaryPath $agentDockBinary
            }
            if ($script:stoppedIds -notcontains 103 -or
                @($script:processes.Keys | Where-Object { $_ -lt 9000 }).Count -ne 0) {
                throw 'Uninstaller left an original or replacement process running.'
            }
            $expectedSurvivors = if ($fullShutdown) { 3 } else { 1 }
            if ($script:processes.Count -ne $expectedSurvivors) {
                throw 'Uninstaller did not preserve processes from the other installation.'
            }
            Write-Host "PASS: process cleanup, replacement Core, and install isolation (full shutdown=$fullShutdown, CIM fallback=$fallback)"
        }
    }
} finally {
    Remove-Item -LiteralPath $testRoot -Recurse -Force -ErrorAction SilentlyContinue
}

Write-Host 'Windows uninstaller process cleanup validation passed.'
