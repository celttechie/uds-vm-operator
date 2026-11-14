<#
.SYNOPSIS
    Applies DISA Windows Server 2022 STIG baseline hardening prior to Sysprep generalization.
.DESCRIPTION
    Supports two complementary mechanisms:
      1. Automated DoD STIG GPO import via LGPO.exe if a STIG GPO package directory is provided.
      2. Direct, idempotent registry, secedit, and auditpol STIG controls that survive `sysprep /generalize`.
    Note: Per-VM unique credentials (IA-5) and NetBIOS ComputerName are applied post-clone via unattend.xml.
#>
[CmdletBinding()]
param(
    [string]$LGPOPath = "C:\UDS\STIG\LGPO.exe",
    [string]$StigGpoDir = "C:\UDS\STIG\GPOs"
)

$ErrorActionPreference = "Stop"

Write-Host "=== DISA Windows Server 2022 STIG Hardening ==="

# 1. Import DoD STIG GPO Backup if LGPO.exe and DoD GPOs are staged
if ((Test-Path $LGPOPath) -and (Test-Path $StigGpoDir)) {
    Write-Host "[1/6] Importing DoD STIG GPOs via LGPO.exe from $StigGpoDir..."
    Get-ChildItem -Path $StigGpoDir -Directory | ForEach-Object {
        Write-Host "  Applying GPO: $($_.Name)"
        & $LGPOPath /g $_.FullName
    }
} else {
    Write-Host "[1/6] LGPO.exe / STIG GPO directory not staged; applying built-in STIG registry & security baselines."
}

# 2. WN22-SO-000145 / FIPS 140-2/3 Cryptographic Algorithms (NIST 800-53 SC-13 / IA-7)
Write-Host "[2/6] Enabling FIPS-validated cryptography and TLS 1.2+..."
$fipsKey = "HKLM:\SYSTEM\CurrentControlSet\Control\Lsa\FipsAlgorithmPolicy"
New-Item -Path $fipsKey -Force | Out-Null
Set-ItemProperty -Path $fipsKey -Name "Enabled" -Value 1 -Type DWord

# Disable legacy SSL 3.0 / TLS 1.0 / TLS 1.1 server & client protocols
foreach ($proto in @("SSL 3.0", "TLS 1.0", "TLS 1.1")) {
    foreach ($side in @("Server", "Client")) {
        $pPath = "HKLM:\SYSTEM\CurrentControlSet\Control\SecurityProviders\SCHANNEL\Protocols\$proto\$side"
        New-Item -Path $pPath -Force | Out-Null
        Set-ItemProperty -Path $pPath -Name "Enabled" -Value 0 -Type DWord
        Set-ItemProperty -Path $pPath -Name "DisabledByDefault" -Value 1 -Type DWord
    }
}

# 3. WN22-CC-000010 / WN22-SO-000100: Disable SMBv1 & Enforce SMB Signing
Write-Host "[3/6] Disabling SMBv1 and enforcing SMB packet signing..."
Disable-WindowsOptionalFeature -Online -FeatureName SMB1Protocol -NoRestart -ErrorAction SilentlyContinue | Out-Null
$smbServer = "HKLM:\SYSTEM\CurrentControlSet\Services\LanmanServer\Parameters"
Set-ItemProperty -Path $smbServer -Name "RequireSecuritySignature" -Value 1 -Type DWord
Set-ItemProperty -Path $smbServer -Name "EnableSecuritySignature" -Value 1 -Type DWord
$smbClient = "HKLM:\SYSTEM\CurrentControlSet\Services\LanmanWorkstation\Parameters"
Set-ItemProperty -Path $smbClient -Name "RequireSecuritySignature" -Value 1 -Type DWord

# 4. WN22-CC-000070 / LSA Protection & Credential Guard Readiness
Write-Host "[4/6] Enabling LSA Protection (RunAsPPL) and disabling WDigest plaintext caching..."
$lsaKey = "HKLM:\SYSTEM\CurrentControlSet\Control\Lsa"
Set-ItemProperty -Path $lsaKey -Name "RunAsPPL" -Value 1 -Type DWord
Set-ItemProperty -Path $lsaKey -Name "NoLMHash" -Value 1 -Type DWord
Set-ItemProperty -Path $lsaKey -Name "LmCompatibilityLevel" -Value 5 -Type DWord

$wdigestKey = "HKLM:\SYSTEM\CurrentControlSet\Control\SecurityProviders\WDigest"
New-Item -Path $wdigestKey -Force | Out-Null
Set-ItemProperty -Path $wdigestKey -Name "UseLogonCredential" -Value 0 -Type DWord

# 5. WN22-AC-000010..000070: Account Lockout & Password Policy via secedit (NIST 800-53 AC-7 / IA-5)
Write-Host "[5/6] Configuring Account Lockout & Password Policy via secedit..."
$secCfgPath = "$env:TEMP\uds-stig-secedit.inf"
$secDbPath  = "$env:TEMP\uds-stig-secedit.sdb"
@"
[Unicode]
Unicode=yes
[System Access]
MinimumPasswordLength = 14
PasswordComplexity = 1
MinimumPasswordAge = 1
MaximumPasswordAge = 60
PasswordHistorySize = 24
LockoutBadCount = 3
ResetLockoutCount = 15
LockoutDuration = 15
EnableGuestAccount = 0
[Version]
signature="`$CHICAGO`$"
Revision=1
"@ | Set-Content -Path $secCfgPath -Encoding Unicode

secedit.exe /configure /db $secDbPath /cfg $secCfgPath /areas SECURITYPOLICY /quiet
Remove-Item -Path $secCfgPath, $secDbPath -Force -ErrorAction SilentlyContinue

# 6. WN22-AU-000010+: Advanced Audit Policy & Windows Firewall Profiles
Write-Host "[6/6] Enabling STIG Advanced Audit Policies and Windows Defender Firewall..."
auditpol.exe /set /category:"Account Logon" /success:enable /failure:enable | Out-Null
auditpol.exe /set /category:"Logon/Logoff" /success:enable /failure:enable | Out-Null
auditpol.exe /set /category:"Policy Change" /success:enable /failure:enable | Out-Null
auditpol.exe /set /category:"Privilege Use" /success:enable /failure:enable | Out-Null
auditpol.exe /set /category:"System" /success:enable /failure:enable | Out-Null

Set-NetFirewallProfile -Profile Domain,Public,Private -Enabled True

# Suppress "Do you want to allow your PC to be discoverable" flyout on first logon
New-Item -Path "HKLM:\SYSTEM\CurrentControlSet\Control\Network\NewNetworkWindowOff" -Force | Out-Null

Write-Host "=== DISA STIG baseline applied successfully (ready for sysprep /generalize) ==="
