@echo off
REM ============================================================================
REM  Qwen-Free-API - one-command launcher (Windows)
REM  First run:  double-click, or:  start.bat
REM ============================================================================
setlocal enabledelayedexpansion
cd /d "%~dp0"

if not exist .env (
    copy .env.example .env >nul
    echo.
    echo  ! -- First-run setup ------------------------------------------
    echo   A starter .env was created from .env.example.
    echo   Open it and paste your chat.qwen.ai token into QWEN_TOKENS:
    echo       notepad .env
    echo       (token: chat.qwen.ai -^> F12 -^> Application -^> Cookies -^> token)
    echo  ----------------------------------------------------------------
    echo.
)

if exist qwen-api.exe (
    set BIN=qwen-api.exe
    goto :run
)

where go >nul 2>nul
if %errorlevel%==0 (
    echo Building qwen-api with Go...
    go build -trimpath -ldflags="-s -w" -o qwen-api.exe .
    set BIN=qwen-api.exe
    goto :run
)

echo No prebuilt binary and no Go toolchain found.
echo Install Go: https://go.dev/dl/   then re-run start.bat
echo Or grab a release binary: https://github.com/Godde3s/qwen-free-api/releases
pause
exit /b 1

:run
for /f "tokens=1,2 delims==" %%a in (.env) do (
    if not "%%b"=="" set "%%a=%%b"
)
echo Starting Qwen-Free-API...
"%BIN%"
