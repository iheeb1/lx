"""Unit tests: failure-cluster ranking (no model needed)."""

import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent.parent / "src"))

from lx_prune.prune import error_windows, rank_clusters, ranked_windows


def lines_with(*failures):
    lines = ["ok %d" % i for i in range(60)]
    for idx, text in failures:
        lines[idx] = text
    return lines


class TestRankClusters(unittest.TestCase):
    def test_single_failure_full_window(self):
        lines = lines_with((10, "FAILED boom"))
        keep, info = ranked_windows(lines, 9)
        self.assertEqual(info["clusters"], 1)
        self.assertIn(1, keep)
        self.assertIn(19, keep)
        self.assertNotIn(0, keep)
        self.assertNotIn(20, keep)

    def test_root_full_cascade_shrunk(self):
        lines = lines_with((10, "FAILED first"), (50, "FAILED cascade"))
        keep, info = ranked_windows(lines, 9)
        self.assertEqual(info, {"clusters": 2, "root_size": 1, "cascade": 1})
        self.assertIn(10, keep)
        self.assertNotIn(40, keep)  # dead middle dropped
        for i in (47, 48, 49, 50, 51, 52, 53):  # shrunk ±3
            self.assertIn(i, keep)
        self.assertNotIn(46, keep)
        self.assertNotIn(54, keep)

    def test_nearby_failures_one_cluster(self):
        lines = lines_with((10, "FAILED a"), (14, "FAILED b"))
        keep, info = ranked_windows(lines, 9)
        self.assertEqual(info["clusters"], 1)
        self.assertIn(5, keep)  # full window around the cluster
        self.assertIn(23, keep)

    def test_no_failures(self):
        keep, info = ranked_windows(["ok"] * 10, 8)
        self.assertEqual(keep, set())
        self.assertEqual(info["clusters"], 0)

    def test_error_windows_legacy(self):
        lines = lines_with((5, "ERROR x"))
        keep = error_windows(lines, 3)
        self.assertEqual(keep, {2, 3, 4, 5, 6, 7, 8})


if __name__ == "__main__":
    unittest.main()
