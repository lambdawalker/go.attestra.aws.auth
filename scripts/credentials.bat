@echo off
call "%~dp0..\deploy.bat" -manage-credentials %*
exit /b %errorlevel%
