#!/usr/bin/env python3
"""Cross-platform release build orchestrator for the Parade desktop application.

Builds the Go daemon binary, stages it as a Tauri sidecar, invokes the Tauri
desktop build, and collects the resulting installers into a dist/ directory.

Usage:
    python scripts/build_release.py

Requirements:
    - pixi-managed toolchain (Go >= 1.26, pnpm >= 9, Rust >= 1.80)
    - Or equivalent tools on PATH: go, pnpm, cargo
"""

import os
import sys
import platform
import subprocess
import shutil
from pathlib import Path

# ---------------------------------------------------------------------------
# Project root (scripts/build_release.py → project root is two levels up)
# ---------------------------------------------------------------------------
PROJECT_ROOT = Path(__file__).resolve().parent.parent


# ===========================================================================
# Platform detection
# ===========================================================================


def detect_platform() -> tuple[str, str, str, str]:
    """Detect the current operating system and CPU architecture.

    Returns a 4-tuple of:
        os_name     – short name: "linux", "macos", or "windows"
        arch        – CPU architecture: "x86_64" or "aarch64"
        target_triple – Rust-style target triple for the sidecar binary name
        exe_suffix  – file extension for executables ("" on Unix, ".exe" on Windows)
    """
    system = sys.platform
    machine = platform.machine()

    # Normalise CPU architecture strings
    arch_map: dict[str, str] = {
        "x86_64": "x86_64",
        "AMD64": "x86_64",        # Windows Python reports AMD64
        "aarch64": "aarch64",
        "arm64": "arm64",          # macOS Python reports arm64
        "ARM64": "aarch64",        # Windows ARM Python reports ARM64
    }
    arch = arch_map.get(machine)
    if arch is None:
        raise RuntimeError(
            f"Unsupported CPU architecture: {machine!r}. "
            f"Expected one of: {', '.join(arch_map.keys())}"
        )

    if system == "linux":
        os_name = "linux"
        if arch == "x86_64":
            triple = "x86_64-unknown-linux-gnu"
        elif arch in ("aarch64", "arm64"):
            triple = "aarch64-unknown-linux-gnu"
        else:
            raise RuntimeError(
                f"Unsupported Linux architecture: {arch!r}"
            )
        exe_suffix = ""
    elif system == "darwin":
        os_name = "macos"
        if arch == "x86_64":
            triple = "x86_64-apple-darwin"
        elif arch == "arm64":
            triple = "aarch64-apple-darwin"
        else:
            raise RuntimeError(
                f"Unsupported macOS architecture: {arch!r}"
            )
        exe_suffix = ""
    elif system in ("win32", "cygwin"):
        os_name = "windows"
        if arch in ("x86_64", "AMD64"):
            triple = "x86_64-pc-windows-msvc"
        elif arch in ("aarch64", "arm64"):
            triple = "aarch64-pc-windows-msvc"
        else:
            raise RuntimeError(
                f"Unsupported Windows architecture: {arch!r}"
            )
        exe_suffix = ".exe"
    else:
        raise RuntimeError(
            f"Unsupported operating system: {system!r}. "
            f"Expected one of: linux, darwin, win32"
        )

    return os_name, arch, triple, exe_suffix


def get_bundles_flag(os_name: str) -> str:
    """Return the Tauri `--bundles` flag value for the given OS.

    Linux → "deb", macOS → "dmg", Windows → "nsis,msi"
    (NSIS produces setup.exe, MSI for enterprise deployment).
    """
    bundles_map: dict[str, str] = {
        "linux": "deb",
        "macos": "dmg",
        "windows": "nsis,msi",
    }
    flag = bundles_map.get(os_name)
    if flag is None:
        raise RuntimeError(f"Unknown OS name for bundles flag: {os_name!r}")
    return flag


# ===========================================================================
# Tool validation
# ===========================================================================


def validate_tools() -> None:
    """Check that required build tools are available on PATH.

    Raises RuntimeError if any required tool is missing.
    """
    required: dict[str, str] = {
        "go": "Go (https://go.dev/dl/)",
        "pnpm": "pnpm (https://pnpm.io/installation)",
        "cargo": "Cargo / Rust (https://rustup.rs/)",
    }

    missing: list[str] = []
    for cmd, label in required.items():
        if shutil.which(cmd) is None:
            missing.append(f"  - {cmd}: {label}")

    if missing:
        raise RuntimeError(
            "Required build tools not found on PATH.\n"
            + "\n".join(missing)
            + "\n\nTip: install tools via pixi (`pixi install`) and run inside `pixi shell`."
        )


# ===========================================================================
# Go daemon build
# ===========================================================================


def build_go(project_root: Path, output_name: str, exe_suffix: str) -> Path:
    """Build the Go daemon binary with release flags.

    Args:
        project_root: Root directory of the Parade project.
        output_name: Base name for the output binary (e.g. "parade").
        exe_suffix: Platform-specific executable suffix ("" or ".exe").

    Returns:
        Path to the built binary.

    Raises:
        subprocess.CalledProcessError: If the Go build fails.
    """
    binary_path = project_root / f"{output_name}{exe_suffix}"

    go_bin = shutil.which("go") or "go"

    cmd: list[str] = [
        go_bin, "build",
        "-o", str(binary_path),
        "-ldflags=-s -w",
        "-trimpath",
        "./cmd/parade/",
    ]

    env = os.environ.copy()
    env["CGO_ENABLED"] = "0"

    print(f"    Command: {' '.join(cmd)}")
    print(f"    CGO_ENABLED=0")
    subprocess.run(cmd, check=True, cwd=str(project_root), env=env)

    if not binary_path.is_file():
        raise RuntimeError(
            f"Go build succeeded but binary not found at: {binary_path}"
        )

    size_mb = binary_path.stat().st_size / (1024 * 1024)
    print(f"    Built: {binary_path} ({size_mb:.1f} MiB)")

    return binary_path


# ===========================================================================
# Tauri sidecar staging
# ===========================================================================


def stage_sidecar(project_root: Path,
                  go_binary: Path,
                  target_triple: str,
                  exe_suffix: str) -> Path:
    """Copy the Go binary into the Tauri sidecar directory.

    Tauri's externalBin config expects the binary at:
        frontend/src-tauri/binaries/parade-daemon-{target_triple}{exe_suffix}

    Args:
        project_root: Root directory of the Parade project.
        go_binary: Path to the Go binary produced by build_go().
        target_triple: Rust target triple (e.g. "x86_64-unknown-linux-gnu").
        exe_suffix: Platform-specific executable suffix ("" or ".exe").

    Returns:
        Path to the staged sidecar binary.
    """
    binaries_dir = project_root / "frontend" / "src-tauri" / "binaries"
    binaries_dir.mkdir(parents=True, exist_ok=True)

    sidecar_name = f"parade-daemon-{target_triple}{exe_suffix}"
    sidecar_path = binaries_dir / sidecar_name

    shutil.copy2(str(go_binary), str(sidecar_path))
    print(f"    Copied: {go_binary} → {sidecar_path}")

    return sidecar_path


# ===========================================================================
# Tauri desktop build
# ===========================================================================


def build_tauri(project_root: Path, bundles_flag: str) -> None:
    """Run the Tauri desktop application build.

    Args:
        project_root: Root directory of the Parade project.
        bundles_flag: Value for `--bundles` (e.g. "deb", "dmg", "msi").

    Raises:
        subprocess.CalledProcessError: If the Tauri build fails.
    """
    frontend_dir = project_root / "frontend"

    pnpm_bin = shutil.which("pnpm") or "pnpm"

    cmd: list[str] = [
        pnpm_bin, "run", "tauri", "build",
        "--bundles", bundles_flag,
    ]

    print(f"    Command: {' '.join(cmd)}")
    print(f"    Working directory: {frontend_dir}")
    subprocess.run(cmd, check=True, cwd=str(frontend_dir))


# ===========================================================================
# Artifact collection
# ===========================================================================


def collect_artifacts(project_root: Path,
                      os_name: str,
                      arch: str,
                      target_triple: str,
                      exe_suffix: str) -> Path:
    """Copy build artifacts into the dist/ output directory.

    Collects:
      - Platform installers (deb/dmg/msi/nsis) from the Tauri bundle dirs.
      - Raw Go daemon binary from the Tauri release bundle.
      - Portable .exe + sidecar (Windows only).

    Args:
        project_root: Root directory of the Parade project.
        os_name: Short OS name ("linux", "macos", "windows").
        arch: CPU architecture ("x86_64" or "aarch64").
        target_triple: Rust target triple for the sidecar binary name.
        exe_suffix: Platform executable suffix ("" or ".exe").

    Returns:
        Path to the dist output directory containing the artifacts.
    """
    base = project_root / "frontend" / "src-tauri" / "target" / "release"
    binaries_dir = project_root / "frontend" / "src-tauri" / "binaries"

    dest_dir = project_root / "dist" / f"parade-{os_name}-{arch}"
    dest_dir.mkdir(parents=True, exist_ok=True)

    files_copied: list[str] = []

    # 1. Copy platform installers from each bundle subdirectory
    bundle_dirs: list[tuple[str, str]] = {
        "linux":   [("bundle/deb", ".deb")],
        "macos":   [("bundle/dmg", ".dmg")],
        "windows": [("bundle/msi", ".msi"), ("bundle/nsis", ".exe")],
    }.get(os_name, [])

    for bundle_subdir, extension in bundle_dirs:
        src_dir = base / bundle_subdir
        if src_dir.is_dir():
            for entry in src_dir.iterdir():
                if entry.is_file() and entry.suffix == extension:
                    dest = dest_dir / entry.name
                    shutil.copy2(str(entry), str(dest))
                    files_copied.append(str(dest))
                    size_mb = entry.stat().st_size / (1024 * 1024)
                    print(f"    Copied: {entry.name} → {dest} ({size_mb:.1f} MiB)")
        else:
            print(f"    Warning: installer directory not found: {src_dir}")

    # 2. Copy the raw Go daemon binary if present in the release bundle
    binary_src_dir = base / "bundle"

    for candidate in [
        binary_src_dir / f"parade-daemon{exe_suffix}",
        binary_src_dir / f"parade{exe_suffix}",
        binary_src_dir / "binary" / f"parade-daemon{exe_suffix}",
        binary_src_dir / "binary" / f"parade{exe_suffix}",
    ]:
        if candidate.is_file():
            dest = dest_dir / candidate.name
            shutil.copy2(str(candidate), str(dest))
            files_copied.append(str(dest))
            size_mb = candidate.stat().st_size / (1024 * 1024)
            print(f"    Copied: {candidate.name} → {dest} ({size_mb:.1f} MiB)")

    # 3. Portable bundle (Windows only): app exe + sidecar, extract-and-run
    if os_name == "windows":
        portable_dir = dest_dir / "portable"
        portable_dir.mkdir(parents=True, exist_ok=True)

        # Copy the Tauri app exe from release dir
        app_exe = base / f"parade_tauri{exe_suffix}"
        if app_exe.is_file():
            dest = portable_dir / app_exe.name
            shutil.copy2(str(app_exe), str(dest))
            files_copied.append(str(dest))
            size_mb = app_exe.stat().st_size / (1024 * 1024)
            print(f"    Copied: {app_exe.name} → {dest} ({size_mb:.1f} MiB)")

            # Copy sidecar alongside the portable exe
            sidecar_name = f"parade-daemon-{target_triple}{exe_suffix}"
            sidecar_src = binaries_dir / sidecar_name
            if sidecar_src.is_file():
                dest = portable_dir / sidecar_name
                shutil.copy2(str(sidecar_src), str(dest))
                files_copied.append(str(dest))
                size_mb = sidecar_src.stat().st_size / (1024 * 1024)
                print(f"    Copied: {sidecar_name} → {dest} ({size_mb:.1f} MiB)")
            else:
                print(f"    Warning: sidecar not found for portable bundle: {sidecar_src}")
        else:
            print(f"    Warning: Tauri app exe not found: {app_exe}")

    if not files_copied:
        print(f"    Warning: no artifacts found to copy from the Tauri bundle.")
        print(f"    Expected location: {base}")

    return dest_dir


# ===========================================================================
# Orchestrator
# ===========================================================================


def main() -> None:
    """Orchestrate the full release build pipeline."""
    steps: list[tuple[str, callable]] = []  # type: ignore[type-arg]

    # ── Step 1: Detect platform ────────────────────────────────────────
    print("[1/6] Detecting platform...")
    try:
        os_name, arch, target_triple, exe_suffix = detect_platform()
    except RuntimeError as exc:
        print(f"\n  Error: {exc}", file=sys.stderr)
        sys.exit(1)

    print(f"    OS:      {os_name}")
    print(f"    Arch:    {arch}")
    print(f"    Triple:  {target_triple}")
    print(f"    Suffix:  {exe_suffix!r}")
    print()

    # ── Step 2: Validate tools ─────────────────────────────────────────
    print("[2/6] Validating build tools...")
    try:
        validate_tools()
    except RuntimeError as exc:
        print(f"\n  Error: {exc}", file=sys.stderr)
        sys.exit(1)
    print("    All required tools found.\n")

    # ── Step 3: Build Go daemon ────────────────────────────────────────
    print("[3/6] Building Go daemon binary...")
    try:
        go_binary = build_go(PROJECT_ROOT, "parade", exe_suffix)
    except FileNotFoundError as exc:
        print(f"\n  Error: 'go' command not found. Is Go installed and on PATH?", file=sys.stderr)
        sys.exit(1)
    except subprocess.CalledProcessError as exc:
        print(f"\n  Go build failed (exit code {exc.returncode}).", file=sys.stderr)
        sys.exit(1)
    print()

    # ── Step 4: Stage sidecar ──────────────────────────────────────────
    print("[4/6] Staging Tauri sidecar binary...")
    try:
        stage_sidecar(PROJECT_ROOT, go_binary, target_triple, exe_suffix)
    except OSError as exc:
        print(f"\n  Error: failed to stage sidecar binary: {exc}", file=sys.stderr)
        sys.exit(1)
    print()

    # ── Step 5: Build Tauri desktop app ────────────────────────────────
    bundles_flag = get_bundles_flag(os_name)
    print(f"[5/6] Building Tauri desktop app (--bundles {bundles_flag})...")
    try:
        build_tauri(PROJECT_ROOT, bundles_flag)
    except FileNotFoundError as exc:
        print(f"\n  Error: 'pnpm' command not found. Is pnpm installed and on PATH?", file=sys.stderr)
        sys.exit(1)
    except subprocess.CalledProcessError as exc:
        print(f"\n  Tauri build failed (exit code {exc.returncode}).", file=sys.stderr)
        sys.exit(1)
    print()

    # ── Step 6: Collect artifacts ──────────────────────────────────────
    print("[6/6] Collecting artifacts to dist/...")
    dest_dir = collect_artifacts(PROJECT_ROOT, os_name, arch,
                                  target_triple, exe_suffix)
    print()

    # ── Summary ────────────────────────────────────────────────────────
    print("=" * 60)
    print("  Release build complete!")
    print(f"  Artifacts at: {dest_dir}")
    print()
    if dest_dir.is_dir():
        contents = sorted(dest_dir.iterdir())
        if contents:
            for item in contents:
                size_mb = item.stat().st_size / (1024 * 1024)
                print(f"    {item.name}  ({size_mb:.1f} MiB)")
        else:
            print("    (no files found)")
    print("=" * 60)


if __name__ == "__main__":
    main()
