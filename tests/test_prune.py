"""Unit tests: pipeline + metrics recording (model disabled via env)."""

import json
import os
import sys
import tempfile
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent.parent / "src"))

from lx_prune import metrics, modes
from lx_prune.prune import prune_text


class IsolatedHome(unittest.TestCase):
    def setUp(self):
        self._tmp = tempfile.TemporaryDirectory()
        self._saved_env = {k: os.environ.get(k) for k in
                           ("LAYA_PRUNE_HOME", "LAYA_PRUNE_CMD", "LAYA_PRUNE_MODE",
                            "LAYA_PRUNE_RC", "LAYA_PRUNE_NO_MODEL", "LAYA_PRUNE_MIN_LINES")}
        os.environ["LAYA_PRUNE_NO_MODEL"] = "1"  # never load the model in unit tests
        os.environ.pop("LAYA_PRUNE_MODE", None)
        os.environ.pop("LAYA_PRUNE_RC", None)
        # Point metrics at a temp dir (module constants are read at call time
        # through the metrics module attributes).
        self._saved_home, self._saved_log, self._saved_runs = metrics.HOME, metrics.LOG, metrics.RUNS
        base = Path(self._tmp.name) / "home"
        metrics.HOME, metrics.LOG, metrics.RUNS = base, base / "metrics.jsonl", base / "runs"
        modes.reset_tune_cache()

    def tearDown(self):
        metrics.HOME, metrics.LOG, metrics.RUNS = self._saved_home, self._saved_log, self._saved_runs
        for k, v in self._saved_env.items():
            if v is None:
                os.environ.pop(k, None)
            else:
                os.environ[k] = v
        modes.reset_tune_cache()
        self._tmp.cleanup()


class TestPipeline(IsolatedHome):
    def test_short_output_untouched(self):
        text = "\n".join(f"line {i}" for i in range(10)) + "\n"
        out, info = prune_text(text, "echo hi")
        self.assertEqual(out, text)
        self.assertEqual(info["model"], "skipped")

    def test_buried_error_and_setup_survive(self):
        from lx_prune import model as model_mod

        body = [f"pkg-{i} fetched in {i}ms" for i in range(150)]
        body.insert(60, "setup: connecting to testdb://localhost db=ci")
        body.insert(61, "tests/test_api.py::test_create FAILED AssertionError: 500 != 201")
        text = "\n".join(body) + "\n"

        class FakeAgent:
            def predict_batch(self, batch, questions):
                out = []
                for item in batch:
                    noisy = "pkg-" in item["output"] and "FAILED" not in item["output"]
                    choice = "B" if noisy else "A"
                    out.append({"answers": {"keep": {
                        "choice": choice, "answer_confidence": 0.9,
                        "probabilities": {"A": 0.9 if choice == "A" else 0.1,
                                          "B": 0.1 if choice == "A" else 0.9}}}})
                return out

        os.environ["LAYA_PRUNE_CMD"] = "pytest"
        os.environ["LAYA_PRUNE_MODE"] = "error"
        os.environ["LAYA_PRUNE_NO_COLLAPSE"] = "1"  # force the chunk/model path
        os.environ.pop("LAYA_PRUNE_NO_MODEL", None)  # use the fake agent
        old_agent = model_mod._agent
        model_mod._agent = FakeAgent()
        try:
            out, info = prune_text(text)
        finally:
            model_mod._agent = old_agent
            os.environ["LAYA_PRUNE_NO_MODEL"] = "1"
            del os.environ["LAYA_PRUNE_NO_COLLAPSE"]
        self.assertIn("AssertionError", out)
        self.assertIn("testdb://", out)  # error neighborhood kept
        self.assertLess(len(out), len(text))
        self.assertEqual(info["mode"], "error")
        self.assertEqual(info["clusters"], 1)
        self.assertGreater(info["dropped_chunks"], 0)

    def test_auto_mode_reads_rc(self):
        os.environ["LAYA_PRUNE_CMD"] = "pytest"
        os.environ["LAYA_PRUNE_RC"] = "2"
        _, info = prune_text("\n".join(f"l{i}" for i in range(5)))
        self.assertEqual(info["mode"], "error")
        self.assertEqual(info["rc"], 2)

    def test_record_stage_and_mode(self):
        os.environ["LAYA_PRUNE_CMD"] = "pytest"
        os.environ["LAYA_PRUNE_MODE"] = "verify"
        rid = metrics.record("a\nb\n", "a\nb\n", {"model": "skipped", "mode": "verify"}, stage="laya")
        recs = metrics.read_log()
        self.assertEqual(len(recs), 1)
        self.assertEqual(recs[0]["id"], rid)
        self.assertEqual(recs[0]["stage"], "laya")
        self.assertEqual(recs[0]["mode"], "verify")

    def test_cmd_tune_dry_run(self):
        for i in range(3):
            metrics.record("x" * 8000, "x" * 7800,
                           {"model": "laya", "scored": 20, "dropped_chunks": 0,
                            "verdicts": {"A": 20, "B": 0, "low_conf": 0}}, stage="laya")
        # silence stdout; just assert it doesn't crash on seeded data
        import io
        from contextlib import redirect_stdout
        os.environ["LAYA_PRUNE_CMD"] = "npm seed"
        buf = io.StringIO()
        with redirect_stdout(buf):
            metrics.cmd_tune(apply=False)
        self.assertIn("noisy", buf.getvalue())
        self.assertIn("model%", buf.getvalue())

    def test_log_rotation_caps_growth(self):
        from lx_prune import config as config_mod
        old_cap = config_mod.MAX_LOG_LINES
        config_mod.MAX_LOG_LINES = 10
        try:
            for i in range(12):
                os.environ["LAYA_PRUNE_CMD"] = f"cmd {i}"
                metrics.record(f"cmd {i}\n", f"cmd {i}\n",
                               {"model": "skipped"}, stage="raw")
            lines = metrics.LOG.read_text().splitlines()
            self.assertLessEqual(len(lines), 10)
            recs = metrics.read_log()
            self.assertEqual(recs[-1]["cmd"], "cmd 11")  # newest survives
        finally:
            config_mod.MAX_LOG_LINES = old_cap


if __name__ == "__main__":
    unittest.main()
