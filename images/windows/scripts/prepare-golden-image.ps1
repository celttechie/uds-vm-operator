<#
.SYNOPSIS
    Prepares a Windows Server VM for Golden Image capture (VirtIO, qemu-ga, updates, STIG cleanup).
.DESCRIPTION
    Intended for Stage 2 Builder VMs or Packer builds prior to `sysprep /generalize /oobe /shutdown`.
#>
[CmdletBinding()]
param(
    [string]$UpdatesDir = "C:\UDS\Updates",
    [switch]$RunSysprep
)

$ErrorActionPreference = "Stop"

Write-Host "[1/4] Installing QEMU Guest Agent from attached virtio-win ISO (if not already installed)..."
$gaService = Get-Service -Name "QEMU-GA" -ErrorAction SilentlyContinue
if (-not $gaService) {
    $msi = Get-ChildItem -Path D:\,E:\,F:\ -Filter "qemu-ga-x86_64.msi" -Recurse -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($msi) {
        Start-Process msiexec.exe -ArgumentList @("/i", $msi.FullName, "/qn", "/norestart") -Wait
        Write-Host "Installed QEMU Guest Agent from $($msi.FullName)"
    } else {
        Write-Warning "qemu-ga-x86_64.msi not found on D:, E:, or F:."
    }
}

Write-Host "[2/4] Applying staged Windows Cumulative Updates (.msu) if present in $UpdatesDir..."
if (Test-Path $UpdatesDir) {
    Get-ChildItem -Path $UpdatesDir -Filter "*.msu" | ForEach-Object {
        Write-Host "Installing update: $($_.Name)"
        Start-Process wusa.exe -ArgumentList @($_.FullName, "/quiet", "/norestart") -Wait
    }
}

Write-Host "[3/4] Applying baseline DISA STIG / security hardening..."
# Disable Guest account and SMBv1
net user Guest /active:no | Out-Null
Disable-WindowsOptionalFeature -Online -FeatureName SMB1Protocol -NoRestart -ErrorAction SilentlyContinue | Out-Null

Write-Host "[4/4] Cleaning WinSxS component store and temp files to minimize QCOW2 size..."
Dism.exe /Online /Cleanup-Image /StartComponentCleanup /ResetBase
Remove-Item -Path "$env:TEMP\*" -Recurse -Force -ErrorAction SilentlyContinue

if ($RunSysprep) {
    Write-Host "Running sysprep /generalize /oobe /shutdown..."
    & "$env:SystemRoot\System32\Sysprep\sysprep.exe" /generalize /oobe /shutdown /quiet
}
