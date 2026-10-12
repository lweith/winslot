# Desktop defaults: the TMRA wallpaper for every user, a STIG-style sign-in
# (typed username, logon notice, idle lock), and the consumer features that
# cost CPU, memory or attention without doing anything for a repeater-site VM
# switched off. Only settings Windows 11 Pro honours (several CloudContent
# policies are Enterprise/Education-only).
#
# provision.ps1 runs it in the golden image. It is idempotent and can be run
# on an existing VM as an administrator; per-user settings then reach users
# created afterwards (they come from the Default profile).
#
# Leaves alone: Windows Update, Defender, Store/App Installer (winget),
# search indexing, telemetry (Pro's minimum is "Required").

param([string]$Wallpaper = 'C:\Windows\Web\Wallpaper\TMRA\tmra.jpg')
$ErrorActionPreference = 'Stop'

function Set-Reg([string]$key, [string]$name, $value, [string]$type = 'DWord') {
  if (-not (Test-Path $key)) { New-Item $key -Force | Out-Null }
  New-ItemProperty $key -Name $name -Value $value -PropertyType $type -Force | Out-Null
}

# --- theme -------------------------------------------------------------------
# tmra.theme: the OS's aero.theme with only the wallpaper lines changed.
$themeFile = 'C:\Windows\Resources\Themes\tmra.theme'
$theme = Get-Content 'C:\Windows\Resources\Themes\aero.theme' | Where-Object { $_ -notmatch '^(Wallpaper|WallpaperStyle|TileWallpaper|DisplayName)=' }
$theme = $theme | ForEach-Object {
  $_
  if ($_ -eq '[Theme]') { 'DisplayName=TMRA' }
  if ($_ -eq '[Control Panel\Desktop]') { "Wallpaper=$Wallpaper"; 'WallpaperStyle=2'; 'TileWallpaper=0' }
}
$theme | Set-Content $themeFile -Encoding Unicode
# Windows applies InstallTheme (Light/Dark by the user's mode) at a new user's
# first sign-in, overwriting the profile's wallpaper; CustomInstallTheme is
# not used for that. firstboot.ps1 sets these again on each clone, after
# Windows setup has run.
$themes = 'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Themes'
foreach ($v in 'InstallTheme', 'InstallThemeLight', 'InstallThemeDark') { Set-Reg $themes $v $themeFile 'String' }
Write-Host "desktop: theme $themeFile"

# --- machine policies ----------------------------------------------------------
$pol = 'HKLM:\SOFTWARE\Policies\Microsoft'
Set-Reg 'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Policies\System' EnableFirstLogonAnimation 0  # "Hi... getting things ready"
# Widgets: its policy key (Dsh) is write-protected by the UCPD driver, so the
# Widgets package is removed below instead.
Set-Reg "$pol\Windows\GameDVR" AllowGameDVR 0                  # Game Bar recording
Set-Reg "$pol\Windows\OneDrive" DisableFileSyncNGSC 1          # OneDrive setup and sync
Set-Reg "$pol\Windows\Windows Search" AllowCloudSearch 0
Set-Reg "$pol\Windows\Windows Search" EnableDynamicContentInWSB 0  # "search highlights"
Set-Reg "$pol\Windows\CloudContent" DisableWindowsConsumerFeatures 1  # Enterprise-only; harmless on Pro
Set-Reg "$pol\Edge" StartupBoostEnabled 0                      # Edge preloaded at sign-in
Set-Reg "$pol\Edge" BackgroundModeEnabled 0                    # Edge running after its windows close
Set-Reg "$pol\Edge" HideFirstRunExperience 1
Write-Host 'desktop: machine policies set'

# --- sign-in (STIG-style) --------------------------------------------------------
# Username and password typed in; no account tiles, no last user, nobody named
# on the lock screen. A notice before every console and RDP sign-in. The
# desktop locks after 15 idle minutes (applications keep running).
$sys = 'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Policies\System'
Set-Reg $sys dontdisplaylastusername 1
Set-Reg $sys DontDisplayLockedUserId 3
Set-Reg $sys InactivityTimeoutSecs 900
Set-Reg $sys LegalNoticeCaption 'TMRA W8HHF repeater-site system' 'String'
Set-Reg $sys LegalNoticeText 'Authorized TMRA administrators and operators only. Activity on this system may be monitored and logged.' 'String'
Write-Host 'desktop: sign-in settings set'

# --- Default profile (every user created from now on) ---------------------------
& reg.exe load 'HKU\WinslotDefault' 'C:\Users\Default\NTUSER.DAT' | Out-Null
if ($LASTEXITCODE -ne 0) { throw 'cannot load the Default profile hive' }
try {
  $u = 'Registry::HKEY_USERS\WinslotDefault'
  $cdm = "$u\Software\Microsoft\Windows\CurrentVersion\ContentDeliveryManager"

  Set-Reg "$u\Control Panel\Desktop" Wallpaper $Wallpaper 'String'
  Set-Reg "$u\Control Panel\Desktop" WallpaperStyle '2' 'String'     # Stretch
  Set-Reg "$u\Control Panel\Desktop" TileWallpaper '0' 'String'
  Set-Reg "$u\Software\Policies\Microsoft\Windows\CloudContent" DisableSpotlightCollectionOnDesktop 1
  Set-Reg "$u\Software\Policies\Microsoft\Windows\Explorer" DisableSearchBoxSuggestions 1  # web results in Start search
  Set-Reg "$u\Software\Sysinternals" EulaAccepted 1

  # Silently installed "suggested" apps, tips, Start and lock-screen ads.
  foreach ($v in 'ContentDeliveryAllowed', 'FeatureManagementEnabled', 'OemPreInstalledAppsEnabled',
                 'PreInstalledAppsEnabled', 'PreInstalledAppsEverEnabled', 'SilentInstalledAppsEnabled',
                 'SoftLandingEnabled', 'SubscribedContentEnabled', 'SystemPaneSuggestionsEnabled',
                 'RotatingLockScreenEnabled', 'RotatingLockScreenOverlayEnabled',
                 'SubscribedContent-310093Enabled', 'SubscribedContent-338387Enabled', 'SubscribedContent-338388Enabled',
                 'SubscribedContent-338389Enabled', 'SubscribedContent-353694Enabled', 'SubscribedContent-353696Enabled') {
    Set-Reg $cdm $v 0
  }
  Set-Reg "$u\Software\Microsoft\Windows\CurrentVersion\UserProfileEngagement" ScoobeSystemSettingEnabled 0  # "finish setting up your device"
  Set-Reg "$u\Software\Microsoft\Windows\CurrentVersion\Explorer\Advanced" Start_IrisRecommendations 0
  Set-Reg "$u\Software\Microsoft\Windows\CurrentVersion\Explorer\Advanced" Start_AccountNotifications 0
  Set-Reg "$u\Software\Microsoft\Windows\CurrentVersion\GameDVR" AppCaptureEnabled 0
  Set-Reg "$u\System\GameConfigStore" GameDVR_Enabled 0

  # Cheaper to draw over VNC/RDP; font smoothing stays on.
  Set-Reg "$u\Software\Microsoft\Windows\CurrentVersion\Themes\Personalize" EnableTransparency 0
  Set-Reg "$u\Software\Microsoft\Windows\CurrentVersion\Explorer\VisualEffects" VisualFXSetting 3  # custom
  Set-Reg "$u\Software\Microsoft\Windows\CurrentVersion\Explorer\Advanced" TaskbarAnimations 0
  Set-Reg "$u\Control Panel\Desktop\WindowMetrics" MinAnimate '0' 'String'

  # OneDrive's per-user installer, run at each new user's first sign-in.
  Remove-ItemProperty "$u\Software\Microsoft\Windows\CurrentVersion\Run" -Name OneDriveSetup -ErrorAction SilentlyContinue
} finally {
  [GC]::Collect(); [GC]::WaitForPendingFinalizers()
  & reg.exe unload 'HKU\WinslotDefault' | Out-Null
}
Write-Host 'desktop: Default profile set'

# --- bundled consumer apps -------------------------------------------------------
# Removed for existing users and from the image (provisioned). A package that
# isn't present is skipped; one Windows refuses to remove is logged, not fatal.
$apps = @(
  'Microsoft.Copilot', 'Clipchamp.Clipchamp', 'Microsoft.BingNews', 'Microsoft.BingWeather', 'Microsoft.BingSearch',
  'Microsoft.GamingApp', 'Microsoft.XboxGamingOverlay', 'Microsoft.XboxIdentityProvider',
  'Microsoft.XboxSpeechToTextOverlay', 'Microsoft.Xbox.TCUI', 'Microsoft.Edge.GameAssist',
  'Microsoft.MicrosoftSolitaireCollection', 'MSTeams', 'Microsoft.OutlookForWindows', 'Microsoft.Todos',
  'Microsoft.MicrosoftOfficeHub', 'Microsoft.Getstarted', 'Microsoft.WindowsFeedbackHub', 'Microsoft.GetHelp',
  'Microsoft.YourPhone', 'MicrosoftWindows.CrossDevice', 'Microsoft.WindowsMaps', 'Microsoft.PowerAutomateDesktop',
  'MicrosoftCorporationII.MicrosoftFamily', 'MicrosoftCorporationII.QuickAssist', 'Microsoft.549981C3F5F10',
  'Microsoft.Windows.DevHome', 'Microsoft.People',
  'MicrosoftWindows.Client.WebExperience'   # Widgets board and its taskbar button
)
$removed = 0
foreach ($a in $apps) {
  foreach ($p in Get-AppxPackage -AllUsers -Name $a) {
    try { Remove-AppxPackage -Package $p.PackageFullName -AllUsers; $removed++ }
    catch { Write-Host "desktop: could not remove $($p.PackageFullName): $_" }
  }
  foreach ($p in Get-AppxProvisionedPackage -Online | Where-Object DisplayName -eq $a) {
    try { Remove-AppxProvisionedPackage -Online -PackageName $p.PackageName -AllUsers | Out-Null; $removed++ }
    catch { Write-Host "desktop: could not deprovision $($p.PackageName): $_" }
  }
}
Write-Host "desktop: $removed consumer app packages removed"
