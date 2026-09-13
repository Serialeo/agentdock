[CmdletBinding()]
param(
    [string] $DownloadScriptPath = ''
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
if ([string]::IsNullOrWhiteSpace($DownloadScriptPath)) {
    $DownloadScriptPath = Join-Path $PSScriptRoot 'test-install-windows-download.ps1'
}
$testRoot = Join-Path ([IO.Path]::GetTempPath()) ('agentdock-download-auth-' + [Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $testRoot | Out-Null
$downloadTestState = @{
    ResponseContent = "Write-Host 'fixture installer'`r`n"
    RemainingFailures = 0
    RequestCount = 0
    LastHeaders = @{}
}

# Mock the transport, not the download wrapper: no network or real token is used.
function Invoke-WebRequest {
    param([switch] $UseBasicParsing, [string] $Uri, [hashtable] $Headers, [string] $OutFile)
    $downloadTestState.RequestCount++
    $downloadTestState.LastHeaders = $Headers
    if ($downloadTestState.RemainingFailures -gt 0) {
        $downloadTestState.RemainingFailures--
        throw 'fixture download failure'
    }
    [IO.File]::WriteAllText($OutFile, $downloadTestState.ResponseContent)
}
function Start-Sleep {
    param([int] $Seconds)
}

try {
    $downloadScript = Join-Path $testRoot 'test-install-windows-download.ps1'
    Copy-Item -LiteralPath $DownloadScriptPath -Destination $downloadScript
    $expected = Join-Path $testRoot 'expected.ps1'
    $output = Join-Path $testRoot 'downloaded.ps1'
    $validationMarker = Join-Path $testRoot 'validated.txt'
    [IO.File]::WriteAllText($expected, "Write-Host 'fixture installer'`n")
    # Verify the real wrapper calls the downstream installer validator only for matching bytes.
    [IO.File]::WriteAllText((Join-Path $testRoot 'test-install-windows.ps1'), @'
param([string] $InstallerPath)
[IO.File]::WriteAllText((Join-Path $PSScriptRoot 'validated.txt'), $InstallerPath)
'@)
    $parameters = @{
        Url = 'https://api.github.com/repos/example/agentdock/contents/scripts/install/install.ps1?ref=fixture'
        ExpectedInstallerPath = $expected
        OutputPath = $output
    }
    $headers = @{
        Authorization = 'Bearer fixture-not-a-real-token'
        Accept = 'application/vnd.github.raw+json'
    }

    & $downloadScript @parameters -Headers $headers
    if ($downloadTestState.RequestCount -ne 1 -or
        $downloadTestState.LastHeaders.Authorization -ne $headers.Authorization -or
        $downloadTestState.LastHeaders.Accept -ne $headers.Accept -or
        -not (Test-Path -LiteralPath $validationMarker)) {
        throw 'Authenticated download did not forward headers and validate normalized content.'
    }
    Write-Host 'PASS: authenticated request, CRLF normalization, downstream validation'

    Remove-Item -LiteralPath $validationMarker
    & $downloadScript @parameters
    if ($downloadTestState.LastHeaders.Count -ne 0 -or -not (Test-Path -LiteralPath $validationMarker)) {
        throw 'Anonymous download compatibility failed.'
    }
    Write-Host 'PASS: anonymous downloads remain supported'

    Remove-Item -LiteralPath $validationMarker
    $downloadTestState.RequestCount = 0
    $downloadTestState.RemainingFailures = 2
    & $downloadScript @parameters -Headers $headers
    if ($downloadTestState.RequestCount -ne 3 -or
        $downloadTestState.LastHeaders.Authorization -ne $headers.Authorization -or
        -not (Test-Path -LiteralPath $validationMarker)) {
        throw 'Retry did not retain authentication and content validation.'
    }
    Write-Host 'PASS: transient retries retain authentication'

    Remove-Item -LiteralPath $validationMarker
    $downloadTestState.ResponseContent = "Write-Host 'wrong installer'`n"
    $rejected = $false
    try {
        & $downloadScript @parameters -Headers $headers
    } catch {
        if ($_.Exception.Message -notlike 'Downloaded installer content does not match*') { throw }
        $rejected = $true
    }
    if (-not $rejected -or (Test-Path -LiteralPath $validationMarker)) {
        throw 'Mismatched download was not rejected before installer validation.'
    }
    Write-Host 'PASS: mismatched content is rejected'

    $downloadTestState.RequestCount = 0
    $downloadTestState.RemainingFailures = 20
    $rejected = $false
    try {
        & $downloadScript @parameters -Headers $headers
    } catch {
        if ($_.Exception.Message -ne 'fixture download failure') { throw }
        $rejected = $true
    }
    if (-not $rejected -or $downloadTestState.RequestCount -ne 10 -or (Test-Path -LiteralPath $validationMarker)) {
        throw 'Permanent download failure must fail after exactly ten attempts.'
    }
    Write-Host 'PASS: permanent download failure cannot report success'
} finally {
    Remove-Item -LiteralPath $testRoot -Recurse -Force
}
