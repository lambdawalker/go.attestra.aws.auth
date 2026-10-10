@echo off
call "%~dp0deploy.bat" -manage-credentials %*
exit /b %errorlevel%
