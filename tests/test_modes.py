"""Unit tests: context modes, families, thresholds (no model needed)."""

import json
import os
import sys
import tempfile
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent.parent / "src"))

from lx_prune import modes


class EnvGuard:
    """Save/restore the env vars tests touch."""

    KEYS = ("LAYA_PRUNE_MODE", "LAYA_PRUNE_RC", "LAYA_PRUNE_CMD",
            "LAYA_PRUNE_FIXED_CONF", "LAYA_PRUNE_MIN_CONF")

    def setUp(self):
        self._saved = {k: os.environ.get(k) for k in self.KEYS}
        for k in self.KEYS:
            os.environ.pop(k, None)
        modes.reset_tune_cache()

    def tearDown(self):
        for k, v in self._saved.items():
            if v is None:
                os.environ.pop(k, None)
            else:
                os.environ[k] = v
        modes.reset_tune_cache()


class TestResolveMode(EnvGuard, unittest.TestCase):
    def test_auto_clean(self):
        os.environ["LAYA_PRUNE_RC"] = "0"
        self.assertEqual(modes.resolve_mode("pytest"), ("auto", 0))

    def test_auto_failure_becomes_error(self):
        os.environ["LAYA_PRUNE_RC"] = "1"
        self.assertEqual(modes.resolve_mode("pytest"), ("error", 1))

    def test_explicit_mode_wins(self):
        os.environ["LAYA_PRUNE_MODE"] = "debug"
        os.environ["LAYA_PRUNE_RC"] = "1"
        self.assertEqual(modes.resolve_mode("pytest"), ("debug", 1))

    def test_bogus_mode_falls_back(self):
        os.environ["LAYA_PRUNE_MODE"] = "bogus"
        self.assertEqual(modes.resolve_mode("pytest")[0], "auto")


class TestThresholds(EnvGuard, unittest.TestCase):
    def test_family_bases(self):
        self.assertEqual(modes.effective_min_conf("pytest"), 0.50)
        self.assertEqual(modes.effective_min_conf("ls -la"), 0.80)

    def test_error_raises_bar(self):
        os.environ["LAYA_PRUNE_MODE"] = "error"
        self.assertEqual(modes.effective_min_conf("pytest"), 0.65)
        self.assertEqual(modes.effective_min_conf("ls -la"), 0.95)

    def test_minimal_is_flat_diet(self):
        os.environ["LAYA_PRUNE_MODE"] = "minimal"
        self.assertEqual(modes.effective_min_conf("pytest"), 0.40)
        self.assertEqual(modes.effective_min_conf("ls -la"), 0.40)

    def test_interactive_never_drops(self):
        self.assertGreater(modes.effective_min_conf("watch ls"), 1.0)

    def test_tune_file_shifts_and_clamps(self):
        with tempfile.TemporaryDirectory() as d:
            tune = Path(d) / "thresholds.json"
            tune.write_text(json.dumps({"noisy": -0.5, "keepy": 0.5}))
            old, modes.TUNE_FILE = modes.TUNE_FILE, tune
            try:
                modes.reset_tune_cache()
                # noisy 0.50 - clamped 0.10 -> 0.40 ; keepy 0.80 + 0.10 -> 0.90
                self.assertEqual(modes.effective_min_conf("pytest"), 0.40)
                self.assertEqual(modes.effective_min_conf("ls"), 0.90)
            finally:
                modes.TUNE_FILE = old
                modes.reset_tune_cache()


class TestFamily(EnvGuard, unittest.TestCase):
    def test_keepy_wins_over_noisy(self):
        self.assertEqual(modes.family_of("git status"), "keepy")
        self.assertEqual(modes.family_of("git log"), "noisy")  # long output: drop more
        self.assertEqual(modes.family_of("npm install"), "noisy")
        self.assertEqual(modes.family_of("echo hi"), "other")


if __name__ == "__main__":
    unittest.main()
