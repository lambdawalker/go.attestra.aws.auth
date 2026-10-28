@echo off
python "%~dp0scripts\export-wizard.py" %*
exit /b %ERRORLEVEL%
