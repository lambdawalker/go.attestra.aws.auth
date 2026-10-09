@echo off
setlocal
powershell.exe -NoLogo -NoProfile -File "%~dp0deploy.ps1" %*
exit /b %errorlevel%
