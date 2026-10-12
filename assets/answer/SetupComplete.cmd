@echo off
rem Runs as SYSTEM on each clone's first boot, after OOBE (baked in by provision.ps1).
powershell.exe -NoProfile -ExecutionPolicy Bypass -File "%WINDIR%\Setup\Scripts\firstboot.ps1"
