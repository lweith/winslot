# Runs once as SYSTEM on each clone's first boot, after OOBE and before any
# logon (called by SetupComplete.cmd; both baked into the image by provision.ps1).
#
#  1. Delete the injected answer file (it holds the clone's admin password).
#  2. Grow C: if the clone's zvol was enlarged.
#  3. Make sshd generate this VM's host keys, then publish the PUBLIC keys on
#     COM1. The host captures COM1 to a file, so admins learn the host keys
#     over a hypervisor-local channel before ever connecting: no TOFU.
#     Lines look like:  HOSTKEY <computername> <keytype> <base64>

Start-Transcript -Path "$env:ProgramData\golden\firstboot.log" -Append | Out-Null

Remove-Item "$env:WINDIR\Panther\Unattend\Unattend.xml" -Force -ErrorAction SilentlyContinue

# The theme a new user gets at first sign-in (desktop.ps1 explains). Set
# again here, after Windows setup, which may have reset it.
$themes = 'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Themes'
foreach ($v in 'InstallTheme', 'InstallThemeLight', 'InstallThemeDark') {
  Set-ItemProperty $themes -Name $v -Value 'C:\Windows\Resources\Themes\tmra.theme'
}

try {
  $max = (Get-PartitionSupportedSize -DriveLetter C).SizeMax
  if ($max -gt (Get-Partition -DriveLetter C).Size + 1GB) { Resize-Partition -DriveLetter C -Size $max }
} catch { Write-Host "resize: $_" }

$ssh = 'C:\ProgramData\ssh'
Start-Service sshd -ErrorAction SilentlyContinue
$deadline = (Get-Date).AddMinutes(3)
while (-not (Test-Path "$ssh\ssh_host_ed25519_key.pub") -and (Get-Date) -lt $deadline) { Start-Sleep 1 }

$lines = Get-ChildItem "$ssh\ssh_host_*_key.pub" | ForEach-Object {
  $f = (Get-Content $_.FullName -Raw).Trim() -split '\s+'
  "HOSTKEY $($env:COMPUTERNAME.ToLower()) $($f[0]) $($f[1])"
}
if (-not $lines) { $lines = @("HOSTKEY $($env:COMPUTERNAME.ToLower()) MISSING no-host-keys-after-3min") }
$lines | Set-Content "$env:ProgramData\golden\hostkeys.txt"
try {
  $com = New-Object System.IO.Ports.SerialPort 'COM1', 115200
  $com.Open(); $lines | ForEach-Object { $com.WriteLine($_) }; $com.Close()
} catch { Write-Host "COM1: $_" }
$lines | ForEach-Object { Write-Host $_ }
Stop-Transcript | Out-Null
