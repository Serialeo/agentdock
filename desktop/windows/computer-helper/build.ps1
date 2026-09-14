param([switch]$Run)

$ErrorActionPreference = 'Stop'
$SourceDir = $PSScriptRoot
$RepoRoot = (Resolve-Path (Join-Path $SourceDir '../../..')).Path
$VsWhere = Join-Path ${env:ProgramFiles(x86)} 'Microsoft Visual Studio/Installer/vswhere.exe'
if (-not (Test-Path $VsWhere)) {
    throw 'Install Visual Studio 2022 or 2026 with Desktop development with C++, a Windows SDK and C++ CMake tools.'
}
$InstanceJson = & $VsWhere -latest -products '*' -version '[17.0,19.0)' -requires Microsoft.VisualStudio.Component.VC.Tools.x86.x64 -format json
if ($LASTEXITCODE -ne 0) { throw 'vswhere failed to inspect Visual Studio installations.' }
$Instances = ($InstanceJson -join "`n") | ConvertFrom-Json
if (-not $Instances) {
    throw 'No supported Visual Studio C++ x64 toolchain found. In Visual Studio Installer, add Desktop development with C++ and a Windows SDK.'
}
$Instance = @($Instances)[0]
$Major = ([version]$Instance.installationVersion).Major
$Generators = @{ 17 = 'Visual Studio 17 2022'; 18 = 'Visual Studio 18 2026' }
$Generator = $Generators[$Major]
if (-not $Generator) { throw "Unsupported Visual Studio version: $($Instance.installationVersion)" }
$Installation = $Instance.installationPath
if ($Instance.instanceId -notmatch '^[a-zA-Z0-9_-]+$') { throw 'Invalid Visual Studio instance ID.' }
# Keep old VS 2022 caches and different installed instances separate.
$BuildDir = Join-Path $RepoRoot "dist/computer-spike-windows/build-vs$Major-$($Instance.instanceId)"
$Candidates = @(Join-Path $Installation 'Common7/IDE/CommonExtensions/Microsoft/CMake/CMake/bin/cmake.exe')
$PathCMake = Get-Command cmake -CommandType Application -ErrorAction SilentlyContinue
if ($PathCMake) { $Candidates += $PathCMake.Source }
$CMake = $null
$CTest = $null
foreach ($Candidate in ($Candidates | Select-Object -Unique)) {
    if (-not (Test-Path $Candidate)) { continue }
    $CandidateCTest = Join-Path (Split-Path $Candidate) 'ctest.exe'
    if (-not (Test-Path $CandidateCTest)) { continue }
    try {
        $CapabilityJson = & $Candidate -E capabilities
        if ($LASTEXITCODE -ne 0) { continue }
        $Capabilities = ($CapabilityJson -join "`n") | ConvertFrom-Json
        if ($Capabilities.generators.name -notcontains $Generator) { continue }
        $CMake = $Candidate
        $CTest = $CandidateCTest
        break
    } catch {
        Write-Warning "Cannot inspect CMake at ${Candidate}: $_"
    }
}
if (-not $CMake) {
    throw "No CMake supports '$Generator' with a matching ctest.exe. VS 2026 requires CMake 4.2 or newer; VS 2022 requires 3.21 or newer. Update C++ CMake tools in Visual Studio Installer, or install a current Windows CMake and add its bin directory to PATH."
}
Write-Host "Visual Studio: $($Instance.displayName) ($Installation)"
Write-Host "Generator: $Generator; CMake: $CMake"
& $CMake -S $SourceDir -B $BuildDir -G $Generator -A x64 "-DCMAKE_GENERATOR_INSTANCE:PATH=$Installation"
if ($LASTEXITCODE -ne 0) { throw 'CMake configuration failed. See the CMake error above; verify that a Windows SDK is installed.' }
& $CMake --build $BuildDir --config Release
if ($LASTEXITCODE -ne 0) { throw 'Build failed. See the compiler error above.' }
& $CTest --test-dir $BuildDir -C Release --output-on-failure
if ($LASTEXITCODE -ne 0) { throw 'Geometry tests failed.' }
$Executable = Join-Path $BuildDir 'Release/AgentDockComputerSpike.exe'
Write-Host "Build completed. Run as your normal desktop user:"
Write-Host "& `"$Executable`""
if ($Run) { Start-Process -FilePath $Executable }
