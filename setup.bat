@echo off
setlocal
where go >nul 2>&1
if errorlevel 1 (
  echo Install Go 1.26.6+ and add it to PATH.
  exit /b 1
)
call go -C "%~dp0tools\deploy" run . -repo-root "%~dp0." -bootstrap %*
exit /b %errorlevel%
