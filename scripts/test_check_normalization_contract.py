from __future__ import annotations

from pathlib import Path
import subprocess
import tempfile
import unittest

from scripts.check_normalization_contract import (
    ACTIVE_GO,
    ACTIVE_PYTHON,
    check_contract,
    versioned_paths,
)


class NormalizationContractGuardTest(unittest.TestCase):
    def setUp(self) -> None:
        self.temp_dir = tempfile.TemporaryDirectory()
        self.repo = Path(self.temp_dir.name)
        self._git("init", "-b", "main")
        self._git("config", "user.email", "ci@example.test")
        self._git("config", "user.name", "CI")
        self._write("README.md", "pre-B-5\n")
        self.pre_baseline = self._commit("pre-baseline")

    def tearDown(self) -> None:
        self.temp_dir.cleanup()

    def test_initial_baseline_is_allowed(self) -> None:
        self._write_profile(1)
        self._commit("baseline")
        self.assertEqual(check_contract(self.repo, self.pre_baseline), [])

    def test_v1_change_cannot_be_hidden_by_updating_a_checksum(self) -> None:
        self._write_profile(1)
        baseline = self._commit("baseline")
        self._write(versioned_paths(1)[1], "changed vectors and a self-declared checksum\n")
        self._commit("rewrite v1")

        errors = check_contract(self.repo, baseline)
        self.assertTrue(any("frozen normalization artifact changed" in item for item in errors))

    def test_active_semantics_require_a_version_increment(self) -> None:
        self._write_profile(1)
        baseline = self._commit("baseline")
        self._write(ACTIVE_GO, self._go_selector(1) + "\n// semantic edit\n")
        self._commit("edit selector")

        errors = check_contract(self.repo, baseline)
        self.assertIn(
            "active normalization changed without incrementing ProfileVersion by exactly one",
            errors,
        )

    def test_version_bump_does_not_unlock_old_profile(self) -> None:
        self._write_profile(1)
        baseline = self._commit("baseline")
        self._write_profile(2)
        self._write(versioned_paths(1)[0], "rewritten v1 implementation\n")
        self._commit("add v2 and rewrite v1")

        errors = check_contract(self.repo, baseline)
        self.assertTrue(any("normalize_v1.go" in item for item in errors))

    def test_new_profile_is_the_only_allowed_semantic_path(self) -> None:
        self._write_profile(1)
        baseline = self._commit("baseline")
        self._write_profile(2)
        self._commit("add v2")
        self.assertEqual(check_contract(self.repo, baseline), [])

    def _write_profile(self, version: int) -> None:
        self._write(ACTIVE_GO, self._go_selector(version))
        self._write(
            ACTIVE_PYTHON,
            f"from .search_key_v{version} import (\n    PROFILE_NAME,\n)\n",
        )
        for path in versioned_paths(version):
            self._write(path, f"semantic profile v{version}: {path}\n")

    @staticmethod
    def _go_selector(version: int) -> str:
        return (
            "package quranutil\n\n"
            f"const ProfileVersion = {version}\n\n"
            f"func NormalizeKey(value string) string {{ return NormalizeKeyV{version}(value) }}\n"
        )

    def _write(self, relative_path: str, content: str) -> None:
        path = self.repo / relative_path
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(content)

    def _commit(self, message: str) -> str:
        self._git("add", ".")
        self._git("commit", "-m", message)
        return self._git("rev-parse", "HEAD").stdout.strip()

    def _git(self, *args: str) -> subprocess.CompletedProcess[str]:
        return subprocess.run(
            ("git", *args),
            cwd=self.repo,
            check=True,
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
        )


if __name__ == "__main__":
    unittest.main()
