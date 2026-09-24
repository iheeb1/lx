"""Unit tests: dedupe + similar-line collapse (no model needed)."""

import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent.parent / "src"))

from lx_prune.collapse import _template, collapse_similar, dedupe_runs


class TestDedupe(unittest.TestCase):
    def test_triple_folds(self):
        out = dedupe_runs(["a", "a", "a", "b"])
        self.assertEqual(out, ["a", "[... previous line repeated 2 more times ...]", "b"])

    def test_pair_kept(self):
        self.assertEqual(dedupe_runs(["a", "a"]), ["a", "a"])


class TestCollapse(unittest.TestCase):
    def test_similar_numbers_collapse(self):
        lines = [f"Downloading chunk {i}/200" for i in range(10)]
        out, dropped = collapse_similar(lines, min_count=6)
        self.assertGreater(dropped, 0)
        self.assertIn(lines[0], out)
        self.assertIn(lines[-1], out)
        self.assertTrue(any("similar lines omitted" in l for l in out))

    def test_error_lines_never_collapse(self):
        lines = [f"ERROR worker {i} died" for i in range(10)]
        out, dropped = collapse_similar(lines, min_count=3)
        self.assertEqual(dropped, 0)
        self.assertEqual(len(out), 10)

    def test_below_min_count_kept(self):
        lines = [f"pkg-{i} fetched" for i in range(4)]
        out, dropped = collapse_similar(lines, min_count=6)
        self.assertEqual(dropped, 0)


class TestTemplate(unittest.TestCase):
    def test_hashes_versions_dates_normalize(self):
        self.assertEqual(_template("commit abc1234def5678 done"), _template("commit 9999888aaa1111 done"))
        self.assertEqual(_template("v1.2.3 released"), _template("v9.9.9 released"))


if __name__ == "__main__":
    unittest.main()
