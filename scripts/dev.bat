@echo off
REM One-command dev launch: Go daemon + Tauri frontend (Windows)
REM Usage: scripts\dev.bat   or   pixi run dev

cd /d "%~dp0\.."

REM Build the Go daemon if not already built
if not exist "parade.exe" (
    echo [dev] Building daemon...
    go build -o parade.exe ./cmd/parade/
    if errorlevel 1 exit /b 1
)

REM Start daemon in a separate console window (so you can see its logs)
echo [dev] Starting daemon in separate window ^(debug mode^)...
start "Parade Daemon" parade.exe daemon --debug

REM Give the daemon a moment to start its IPC pipe
echo [dev] Waiting for daemon to start...
timeout /t 2 /nobreak >nul

REM Start Tauri in dev mode (compiles Rust + opens WebView)
echo [dev] Starting Tauri frontend...
cd frontend
pnpm run tauri dev

REM Tauri exited — kill the daemon
echo [dev] Stopping daemon...
taskkill /F /FI "WINDOWTITLE eq Parade Daemon" 2>nul
