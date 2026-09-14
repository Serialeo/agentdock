$ErrorActionPreference = 'Stop'
$SourceDir = $PSScriptRoot
$RepoRoot = (Resolve-Path (Join-Path $SourceDir '../../..')).Path
$BuildDir = Join-Path $RepoRoot 'dist/computer-spike-windows/build'
$CMakeCommand = Get-Command cmake -ErrorAction SilentlyContinue
if ($CMakeCommand) {
    $CMake = $CMakeCommand.Source
} else {
    $VsWhere = Join-Path ${env:ProgramFiles(x86)} 'Microsoft Visual Studio/Installer/vswhere.exe'
    if (-not (Test-Path $VsWhere)) { throw 'Install Visual Studio 2022 Build Tools with Desktop development with C++ and CMake tools.' }
    $Installation = & $VsWhere -latest -products '*' -requires Microsoft.VisualStudio.Component.VC.Tools.x86.x64 -property installationPath
    if (-not $Installation) { throw 'Visual Studio C++ x64 toolchain not found.' }
    $CMake = Join-Path $Installation 'Common7/IDE/CommonExtensions/Microsoft/CMake/CMake/bin/cmake.exe'
    if (-not (Test-Path $CMake)) { throw 'Install the C++ CMake tools component in Visual Studio Installer.' }
}
& $CMake -S $SourceDir -B $BuildDir -G 'Visual Studio 17 2022' -A x64
if ($LASTEXITCODE -ne 0) { throw 'CMake configuration failed.' }
& $CMake --build $BuildDir --config Release
if ($LASTEXITCODE -ne 0) { throw 'Build failed.' }
$CTest = Join-Path (Split-Path $CMake) 'ctest.exe'
& $CTest --test-dir $BuildDir -C Release --output-on-failure
if ($LASTEXITCODE -ne 0) { throw 'Geometry tests failed.' }
$Executable = Join-Path $BuildDir 'Release/AgentDockComputerSpike.exe'
Write-Host "Build completed. Run as your normal desktop user:"
Write-Host "& `"$Executable`""
