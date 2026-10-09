@echo off
setlocal
powershell.exe -NoLogo -NoProfile -File "%~dp0setup-github.ps1"
exit /b %errorlevel%
