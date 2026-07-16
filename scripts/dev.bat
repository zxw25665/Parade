@echo off
REM One-command dev launch: Go daemon + Tauri frontend (Windows)
REM Usage: scripts\dev.bat   or   pixi run dev
REM
REM The daemon is spawned automatically by Tauri's Rust bridge
REM (src-tauri\src\lib.rs → spawn_daemon). We only need to build
REM and stage the sidecar binary.

cd /d "%~dp0\.."

REM Kill any leftover daemon processes from a previous run
taskkill /F /IM parade.exe 2>nul
taskkill /F /IM parade-daemon.exe 2>nul
timeout /t 1 /nobreak >nul

REM Build the Go daemon and stage it as a Tauri sidecar
echo [dev] Building daemon...
go build -o parade.exe ./cmd/parade/
if errorlevel 1 exit /b 1

REM Copy to Tauri sidecar location (required by externalBin in tauri.conf.json)
if not exist "frontend\src-tauri\binaries" mkdir "frontend\src-tauri\binaries"
copy /Y parade.exe "frontend\src-tauri\binaries\parade-daemon-x86_64-pc-windows-msvc.exe" >nul
if errorlevel 1 (
    echo [dev] Failed to copy sidecar binary
    exit /b 1
)
echo [dev] Sidecar staged

REM Start Tauri in dev mode (compiles Rust + opens WebView).
REM Tauri's Rust bridge will spawn the daemon automatically.
echo [dev] Starting Tauri frontend...
cd frontend
pnpm run tauri dev
