# Runs once in Audit Mode (auditUser pass) as the built-in Administrator.
# Everything it installs comes from the WINCFG ISO; the build VM's NIC is
# link-down, so nothing here touches the network (no Windows Update, no Store
# updates that would break Sysprep). Ends in sysprep /generalize /oobe and a
# shutdown; on any failure it stops and leaves the VM running for inspection.
#
# Progress goes to C:\ProgramData\golden\provision.log and to COM1, which the
# host captures in the serial log that build.sh tails.

$ErrorActionPreference = 'Stop'
$src   = $PSScriptRoot
$pay   = Join-Path $src 'payload'
$state = 'C:\ProgramData\golden'
New-Item -ItemType Directory -Force $state | Out-Null
Start-Transcript -Path "$state\provision.log" -Append | Out-Null

$com = $null
try { $com = New-Object System.IO.Ports.SerialPort 'COM1', 115200; $com.Open() } catch { $com = $null }
function Log([string]$msg) {
  $line = "GOLDEN $(Get-Date -Format s) $msg"
  Write-Host $line
  if ($com) { try { $com.WriteLine($line) } catch {} }
}

function Run([string]$what, [string]$exe, [string[]]$argv) {
  Log "install $what"
  $p = Start-Process -FilePath $exe -ArgumentList $argv -Wait -PassThru -NoNewWindow
  if ($p.ExitCode -notin 0, 1641, 3010) { throw "$what failed: exit $($p.ExitCode)" }
  Log "  $what ok (exit $($p.ExitCode))"
}
function Msi([string]$what, [string]$file, [string[]]$props = @()) {
  Run $what 'msiexec.exe' (@('/i', "`"$(Join-Path $pay $file)`"", '/qn', '/norestart', '/l*v', "`"$state\$what.msi.log`"") + $props)
}
function Exe([string]$what, [string]$file, [string[]]$argv) {
  Run $what (Join-Path $pay $file) $argv
}
function FwAllow([string]$name, [int]$port) {
  Get-NetFirewallRule -Name $name -ErrorAction SilentlyContinue | Remove-NetFirewallRule
  New-NetFirewallRule -Name $name -DisplayName $name -Direction Inbound -Protocol TCP `
    -LocalPort $port -Action Allow -Profile Any | Out-Null
}

try {
  Log "provision start, media $src"

  # --- hypervisor integration ------------------------------------------------
  # Drivers for every virtio device (incl. NetKVM, balloon, serial) into the
  # driver store, so clones with any virtio hardware come up without prompts.
  Msi 'virtio-win-gt' 'virtio-win-gt-x64.msi'
  Msi 'qemu-ga'       'qemu-ga-x86_64.msi'
  Set-Service QEMU-GA -StartupType Automatic

  # --- applications ----------------------------------------------------------
  Msi 'firefox'    'firefox.msi'
  Msi '7zip'       '7zip.msi'
  Msi 'powershell' 'powershell.msi' @('ADD_PATH=1', 'USE_MU=0', 'ENABLE_MU=0', 'REGISTER_MANIFEST=1')
  Exe 'teraterm'   'teraterm.exe'   @('/ALLUSERS', '/VERYSILENT', '/SUPPRESSMSGBOXES', '/NORESTART', '/SP-')
  Exe 'notepadpp'  'notepadpp.exe'  @('/S')
  Exe 'git'        'git.exe'        @('/VERYSILENT', '/SUPPRESSMSGBOXES', '/NORESTART', '/NOCANCEL', '/SP-')

  Log 'install sysinternals'
  $si = 'C:\Tools\Sysinternals'
  New-Item -ItemType Directory -Force $si | Out-Null
  Expand-Archive -Path (Join-Path $pay 'SysinternalsSuite.zip') -DestinationPath $si -Force
  $mp = [Environment]::GetEnvironmentVariable('Path', 'Machine')
  if ($mp -notlike "*$si*") { [Environment]::SetEnvironmentVariable('Path', "$mp;$si", 'Machine') }

  Log "  sysinternals ok ($((Get-ChildItem $si -Filter *.exe).Count) exes)"

  # --- monitoring ------------------------------------------------------------
  # The defaults plus license (Windows activation state). Listed out: msiexec
  # would expand a "[defaults]" placeholder as a property reference.
  Msi 'windows_exporter' 'windows_exporter.msi' @('LISTEN_PORT=9182', 'ENABLED_COLLECTORS=cpu,memory,logical_disk,physical_disk,net,os,service,system,license')
  Set-Service windows_exporter -StartupType Automatic
  FwAllow 'golden-windows_exporter' 9182

  # --- remote access ---------------------------------------------------------
  Msi 'openssh' 'openssh.msi'
  Set-Service sshd -StartupType Automatic
  New-Item 'HKLM:\SOFTWARE\OpenSSH' -Force | Out-Null
  New-ItemProperty 'HKLM:\SOFTWARE\OpenSSH' -Name DefaultShell -PropertyType String -Force `
    -Value 'C:\Program Files\PowerShell\7\pwsh.exe' | Out-Null
  $keys = 'C:\ProgramData\ssh\administrators_authorized_keys'
  New-Item -ItemType Directory -Force (Split-Path $keys) | Out-Null
  Copy-Item (Join-Path $src 'authorized_keys') $keys -Force
  & icacls.exe $keys /inheritance:r /grant '*S-1-5-32-544:F' /grant '*S-1-5-18:F' | Out-Null
  FwAllow 'golden-sshd' 22

  # RDP with NLA. Rules opened on every profile: a libvirt NAT network is an
  # "unidentified" network, which Windows classifies as Public.
  $ts = 'HKLM:\SYSTEM\CurrentControlSet\Control\Terminal Server'
  Set-ItemProperty $ts -Name fDenyTSConnections -Value 0
  Set-ItemProperty "$ts\WinStations\RDP-Tcp" -Name UserAuthentication -Value 1
  Set-ItemProperty "$ts\WinStations\RDP-Tcp" -Name SecurityLayer -Value 2
  Get-NetFirewallRule -Group '@FirewallAPI.dll,-28752' | Set-NetFirewallRule -Enabled True -Profile Any
  Log '  remote access configured'

  # --- host behaviour --------------------------------------------------------
  & powercfg.exe /hibernate off                       # also disables Fast Startup
  & powercfg.exe /change standby-timeout-ac 0
  & powercfg.exe /change monitor-timeout-ac 0
  # --- desktop ---------------------------------------------------------------
  # TMRA wallpaper (stretched) for every user, consumer features and apps off,
  # STIG-style sign-in: see desktop.ps1.
  New-Item -ItemType Directory -Force 'C:\Windows\Web\Wallpaper\TMRA' | Out-Null
  Copy-Item (Join-Path $src 'wallpaper.jpg') 'C:\Windows\Web\Wallpaper\TMRA\tmra.jpg' -Force
  Log 'install desktop defaults'
  # 6>&1: desktop.ps1 reports with Write-Host (the information stream).
  & (Join-Path $src 'desktop.ps1') 6>&1 | ForEach-Object { Log "  $_" }

  # --- clone first-boot hook -------------------------------------------------
  # SetupComplete.cmd -> firstboot.ps1 runs as SYSTEM after OOBE on every
  # clone: drops the injected answer file, grows C:, publishes the new SSH
  # host keys on COM1 (see firstboot.ps1).
  New-Item -ItemType Directory -Force 'C:\Windows\Setup\Scripts' | Out-Null
  Copy-Item (Join-Path $src 'SetupComplete.cmd') 'C:\Windows\Setup\Scripts\SetupComplete.cmd' -Force
  Copy-Item (Join-Path $src 'firstboot.ps1') 'C:\Windows\Setup\Scripts\firstboot.ps1' -Force

  # --- seal ------------------------------------------------------------------
  Copy-Item (Join-Path $pay 'MANIFEST') "$state\MANIFEST" -Force
  Copy-Item (Join-Path $src 'BUILD') "$state\BUILD" -Force

  # Files copied off the ISO inherit the CD's ReadOnly attribute, which blocks
  # later in-place updates (e.g. pushing a new administrators_authorized_keys).
  Get-Item $keys, "$state\MANIFEST", "$state\BUILD", 'C:\Windows\Setup\Scripts\SetupComplete.cmd',
           'C:\Windows\Setup\Scripts\firstboot.ps1', 'C:\Windows\Web\Wallpaper\TMRA\tmra.jpg' | ForEach-Object { $_.IsReadOnly = $false }

  # Host keys must be unique per clone; sshd regenerates missing ones on start.
  Stop-Service sshd -ErrorAction SilentlyContinue
  Remove-Item 'C:\ProgramData\ssh\ssh_host_*' -Force -ErrorAction SilentlyContinue
  # Sysprep /generalize refuses (0x80073cf2) if any Appx package is installed
  # for a user but not provisioned. Notepad++ registers a per-user sparse
  # package for its Win11 context-menu entry; installers that do this get
  # their package dropped here (the app itself keeps working).
  $prov = (Get-AppxProvisionedPackage -Online).DisplayName
  Get-AppxPackage -AllUsers | Where-Object {
    $_.SignatureKind -ne 'System' -and -not $_.IsFramework -and $_.Name -notin $prov
  } | ForEach-Object {
    Log "  removing unprovisioned appx $($_.PackageFullName)"
    Remove-AppxPackage -Package $_.PackageFullName -AllUsers
  }
  # The cached build answer file says Reseal=Audit; it must not reach clones.
  Remove-Item 'C:\Windows\Panther\unattend.xml', 'C:\Windows\Panther\Unattend\*' -Force -ErrorAction SilentlyContinue

  Log 'provision complete, running sysprep'
  Get-Process sysprep -ErrorAction SilentlyContinue | Stop-Process -Force   # Audit Mode auto-opens the GUI
  Stop-Transcript | Out-Null
  $p = Start-Process "$env:windir\System32\Sysprep\sysprep.exe" -Wait -PassThru `
         -ArgumentList '/generalize', '/oobe', '/shutdown', '/quiet', '/mode:vm'
  Log "sysprep returned $($p.ExitCode) (expected only on failure; see C:\Windows\System32\Sysprep\Panther\setuperr.log)"
  if (Test-Path "$env:windir\System32\Sysprep\Panther\setuperr.log") {
    Get-Content "$env:windir\System32\Sysprep\Panther\setuperr.log" | ForEach-Object { Log "setuperr: $_" }
  }
} catch {
  Log "FAILED: $_"
  try { Stop-Transcript | Out-Null } catch {}
  exit 1
}
