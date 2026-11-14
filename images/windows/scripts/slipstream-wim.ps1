<#
.SYNOPSIS
    Offline DISM slipstreaming script for Windows Server install.wim or mounted VHDX.
.DESCRIPTION
    Injects VirtIO storage/network drivers and Cumulative Updates (.msu/.cab) offline
    before ISO packaging or Golden Image capture.
#>
[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string]$WimPath,
    [int]$ImageIndex = 2,
    [string]$MountDir = "C:\Mount\Win2022",
    [Parameter(Mandatory = $true)]
    [string]$VirtioDriversPath,
    [string]$UpdatesPath = ""
)

$ErrorActionPreference = "Stop"

New-Item -ItemType Directory -Path $MountDir -Force | Out-Null

try {
    Write-Host "Mounting $WimPath (Index $ImageIndex) to $MountDir..."
    Mount-WindowsImage -ImagePath $WimPath -Index $ImageIndex -Path $MountDir

    Write-Host "Slipstreaming VirtIO drivers from $VirtioDriversPath..."
    Add-WindowsDriver -Path $MountDir -Driver $VirtioDriversPath -Recurse -ForceUnsigned:$false

    if ($UpdatesPath -and (Test-Path $UpdatesPath)) {
        Write-Host "Slipstreaming OS packages from $UpdatesPath..."
        Get-ChildItem -Path $UpdatesPath -Include "*.msu", "*.cab" -Recurse | ForEach-Object {
            Write-Host "  Adding package: $($_.Name)"
            Add-WindowsPackage -Path $MountDir -PackagePath $_.FullName
        }
    }

    Write-Host "Committing and dismounting image..."
    Dismount-WindowsImage -Path $MountDir -Save
}
catch {
    Write-Error "Slipstream failed: $_"
    Dismount-WindowsImage -Path $MountDir -Discard -ErrorAction SilentlyContinue
    throw
}
