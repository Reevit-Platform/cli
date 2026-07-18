import json
import sys
import tempfile
import unittest
from pathlib import Path


SCRIPTS_DIR = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(SCRIPTS_DIR))

from sdk_release_versions import next_version, update_version  # noqa: E402


class NextVersionTests(unittest.TestCase):
    def test_calculates_patch_minor_and_major_versions(self):
        self.assertEqual(next_version("0.10.1", "patch"), "0.10.2")
        self.assertEqual(next_version("0.10.1", "minor"), "0.11.0")
        self.assertEqual(next_version("0.10.1", "major"), "1.0.0")

    def test_rejects_invalid_versions_and_levels(self):
        with self.assertRaisesRegex(ValueError, "stable semantic version"):
            next_version("0.10.1-beta.1", "patch")
        with self.assertRaisesRegex(ValueError, "bump level"):
            next_version("0.10.1", "banana")


class UpdateVersionTests(unittest.TestCase):
    def test_updates_npm_manifest_and_root_lockfile_versions_only(self):
        with tempfile.TemporaryDirectory() as temporary_directory:
            root = Path(temporary_directory)
            (root / "package.json").write_text(
                json.dumps({"name": "@reevit/example", "version": "0.10.1"}, indent=2)
                + "\n"
            )
            (root / "package-lock.json").write_text(
                json.dumps(
                    {
                        "name": "@reevit/example",
                        "version": "0.10.0",
                        "packages": {
                            "": {"name": "@reevit/example", "version": "0.10.0"},
                            "node_modules/tool": {
                                "version": "1.0.0",
                                "engines": {"node": ">=0.10.0"},
                            },
                        },
                    },
                    indent=2,
                )
                + "\n"
            )

            changed = update_version(
                "npm", root, "package.json", "@reevit/example", "0.10.2"
            )

            manifest = json.loads((root / "package.json").read_text())
            lockfile = json.loads((root / "package-lock.json").read_text())
            self.assertEqual(manifest["version"], "0.10.2")
            self.assertEqual(lockfile["version"], "0.10.2")
            self.assertEqual(lockfile["packages"][""]["version"], "0.10.2")
            self.assertEqual(
                lockfile["packages"]["node_modules/tool"]["engines"]["node"],
                ">=0.10.0",
            )
            self.assertEqual(changed, [Path("package.json"), Path("package-lock.json")])

    def test_updates_python_single_source_version(self):
        with tempfile.TemporaryDirectory() as temporary_directory:
            root = Path(temporary_directory)
            version_file = root / "reevit" / "_version.py"
            version_file.parent.mkdir()
            version_file.write_text('__version__ = "0.9.1"\n')

            changed = update_version(
                "pypi", root, "reevit/_version.py", "reevit", "0.9.2"
            )

            self.assertEqual(version_file.read_text(), '__version__ = "0.9.2"\n')
            self.assertEqual(changed, [Path("reevit/_version.py")])

    def test_updates_rust_manifest_and_workspace_package_lock_entry(self):
        with tempfile.TemporaryDirectory() as temporary_directory:
            root = Path(temporary_directory)
            (root / "Cargo.toml").write_text(
                '[package]\nname = "reevit"\nversion = "0.1.0"\n\n'
                '[dependencies]\nexample = "0.1.0"\n'
            )
            (root / "Cargo.lock").write_text(
                '[[package]]\nname = "example"\nversion = "0.1.0"\n\n'
                '[[package]]\nname = "reevit"\nversion = "0.1.0"\n'
            )

            changed = update_version(
                "crates", root, "Cargo.toml", "reevit", "0.1.1"
            )

            self.assertIn('version = "0.1.1"', (root / "Cargo.toml").read_text())
            self.assertIn(
                'name = "example"\nversion = "0.1.0"',
                (root / "Cargo.lock").read_text(),
            )
            self.assertIn(
                'name = "reevit"\nversion = "0.1.1"',
                (root / "Cargo.lock").read_text(),
            )
            self.assertEqual(changed, [Path("Cargo.toml"), Path("Cargo.lock")])

    def test_rust_update_is_atomic_when_lock_entry_is_missing(self):
        with tempfile.TemporaryDirectory() as temporary_directory:
            root = Path(temporary_directory)
            manifest = '[package]\nname = "reevit"\nversion = "0.1.0"\n'
            lockfile = '[[package]]\nname = "example"\nversion = "0.1.0"\n'
            (root / "Cargo.toml").write_text(manifest)
            (root / "Cargo.lock").write_text(lockfile)

            with self.assertRaisesRegex(ValueError, "expected one 'reevit'"):
                update_version("crates", root, "Cargo.toml", "reevit", "0.1.1")

            self.assertEqual((root / "Cargo.toml").read_text(), manifest)
            self.assertEqual((root / "Cargo.lock").read_text(), lockfile)

    def test_tag_driven_packages_do_not_modify_manifests(self):
        with tempfile.TemporaryDirectory() as temporary_directory:
            root = Path(temporary_directory)
            (root / "composer.json").write_text('{"name":"reevit/reevit-php"}\n')

            changed = update_version(
                "packagist", root, "composer.json", "reevit/reevit-php", "0.2.0"
            )

            self.assertEqual(changed, [])


if __name__ == "__main__":
    unittest.main()
