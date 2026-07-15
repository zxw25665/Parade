"""Unit tests for build_release.py helper functions.

Run via:
    python -m pytest scripts/test_build_release.py -v
    python -m unittest scripts/test_build_release.py -v
"""

import sys
import unittest
from pathlib import Path
from unittest.mock import patch

# Import the module under test
sys.path.insert(0, str(Path(__file__).resolve().parent))
from build_release import detect_platform, get_bundles_flag


class TestDetectPlatform(unittest.TestCase):
    """Test platform/arch → target triple + exe suffix mapping."""

    def test_linux_x86_64(self):
        with patch("build_release.sys.platform", "linux"), \
             patch("build_release.platform.machine", return_value="x86_64"):
            os_name, arch, triple, suffix = detect_platform()
            self.assertEqual(os_name, "linux")
            self.assertEqual(arch, "x86_64")
            self.assertEqual(triple, "x86_64-unknown-linux-gnu")
            self.assertEqual(suffix, "")

    def test_linux_aarch64(self):
        with patch("build_release.sys.platform", "linux"), \
             patch("build_release.platform.machine", return_value="aarch64"):
            os_name, arch, triple, suffix = detect_platform()
            self.assertEqual(os_name, "linux")
            self.assertEqual(triple, "aarch64-unknown-linux-gnu")
            self.assertEqual(suffix, "")

    def test_linux_arm64(self):
        with patch("build_release.sys.platform", "linux"), \
             patch("build_release.platform.machine", return_value="arm64"):
            os_name, arch, triple, suffix = detect_platform()
            self.assertEqual(os_name, "linux")
            self.assertEqual(triple, "aarch64-unknown-linux-gnu")
            self.assertEqual(suffix, "")

    def test_macos_x86_64(self):
        with patch("build_release.sys.platform", "darwin"), \
             patch("build_release.platform.machine", return_value="x86_64"):
            os_name, arch, triple, suffix = detect_platform()
            self.assertEqual(os_name, "macos")
            self.assertEqual(arch, "x86_64")
            self.assertEqual(triple, "x86_64-apple-darwin")
            self.assertEqual(suffix, "")

    def test_macos_arm64(self):
        with patch("build_release.sys.platform", "darwin"), \
             patch("build_release.platform.machine", return_value="arm64"):
            os_name, arch, triple, suffix = detect_platform()
            self.assertEqual(os_name, "macos")
            self.assertEqual(triple, "aarch64-apple-darwin")
            self.assertEqual(suffix, "")

    def test_windows_x86_64(self):
        with patch("build_release.sys.platform", "win32"), \
             patch("build_release.platform.machine", return_value="AMD64"):
            os_name, arch, triple, suffix = detect_platform()
            self.assertEqual(os_name, "windows")
            self.assertEqual(arch, "x86_64")
            self.assertEqual(triple, "x86_64-pc-windows-msvc")
            self.assertEqual(suffix, ".exe")

    def test_windows_aarch64(self):
        with patch("build_release.sys.platform", "win32"), \
             patch("build_release.platform.machine", return_value="aarch64"):
            os_name, arch, triple, suffix = detect_platform()
            self.assertEqual(os_name, "windows")
            self.assertEqual(arch, "aarch64")
            self.assertEqual(triple, "aarch64-pc-windows-msvc")
            self.assertEqual(suffix, ".exe")

    def test_windows_arm64(self):
        with patch("build_release.sys.platform", "win32"), \
             patch("build_release.platform.machine", return_value="ARM64"):
            os_name, arch, triple, suffix = detect_platform()
            self.assertEqual(os_name, "windows")
            self.assertEqual(triple, "aarch64-pc-windows-msvc")
            self.assertEqual(suffix, ".exe")

    def test_cygwin_treated_as_windows(self):
        with patch("build_release.sys.platform", "cygwin"), \
             patch("build_release.platform.machine", return_value="x86_64"):
            os_name, _, triple, suffix = detect_platform()
            self.assertEqual(os_name, "windows")
            self.assertEqual(triple, "x86_64-pc-windows-msvc")
            self.assertEqual(suffix, ".exe")

    def test_unsupported_os_raises(self):
        with patch("build_release.sys.platform", "freebsd"), \
             patch("build_release.platform.machine", return_value="x86_64"):
            with self.assertRaises(RuntimeError):
                detect_platform()

    def test_unsupported_arch_raises(self):
        with patch("build_release.sys.platform", "linux"), \
             patch("build_release.platform.machine", return_value="riscv64"):
            with self.assertRaises(RuntimeError):
                detect_platform()

    def test_unsupported_linux_arch_raises(self):
        with patch("build_release.sys.platform", "linux"), \
             patch("build_release.platform.machine", return_value="mips64"):
            with self.assertRaises(RuntimeError):
                detect_platform()

    def test_unsupported_macos_arch_raises(self):
        with patch("build_release.sys.platform", "darwin"), \
             patch("build_release.platform.machine", return_value="ppc64"):
            with self.assertRaises(RuntimeError):
                detect_platform()

    def test_unsupported_windows_arch_raises(self):
        with patch("build_release.sys.platform", "win32"), \
             patch("build_release.platform.machine", return_value="mips"):
            with self.assertRaises(RuntimeError):
                detect_platform()


class TestBundlesFlag(unittest.TestCase):
    """Test Tauri --bundles flag mapping per OS."""

    def test_linux_returns_deb(self):
        self.assertEqual(get_bundles_flag("linux"), "deb")

    def test_macos_returns_dmg(self):
        self.assertEqual(get_bundles_flag("macos"), "dmg")

    def test_windows_returns_msi(self):
        self.assertEqual(get_bundles_flag("windows"), "msi")

    def test_unknown_os_raises(self):
        with self.assertRaises(RuntimeError):
            get_bundles_flag("freebsd")


if __name__ == "__main__":
    unittest.main(verbosity=2)
