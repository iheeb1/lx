"""Unit tests: editor_setup install/uninstall (sandboxed HOME, no side effects)."""

import json
import sys
import tempfile
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent.parent / "scripts"))

import editor_setup as es

REPO = str(Path(__file__).resolve().parent.parent)
BIN = "/tmp/fakebin"


class Sandbox(unittest.TestCase):
    def setUp(self):
        self._tmp = tempfile.TemporaryDirectory()
        self.home = str(Path(self._tmp.name) / "home")

    def tearDown(self):
        self._tmp.cleanup()

    def install(self, *feats):
        msg = ""
        for f in feats:
            msg = es.FEATURES[f][0](self.home, REPO, BIN)
        return msg

    def uninstall(self, *feats):
        msg = ""
        for f in feats:
            msg = es.FEATURES[f][1](self.home, REPO, BIN)
        return msg


class TestClaudeCode(Sandbox):
    SETTINGS = None

    def setUp(self):
        super().setUp()
        self.SETTINGS = Path(self.home) / ".claude" / "settings.json"
        self.SETTINGS.parent.mkdir(parents=True)
        self.SETTINGS.write_text(json.dumps({"hooks": {"PreToolUse": []}, "theme": "dark"}))

    def test_install_merges_preserves(self):
        msg = self.install("claude-code")
        data = json.loads(self.SETTINGS.read_text())
        self.assertEqual(data["theme"], "dark")
        self.assertEqual(data["hooks"]["PreToolUse"], [])
        post = data["hooks"]["PostToolUse"]
        self.assertEqual(len(post), 1)
        self.assertEqual(post[0]["matcher"], "Bash")
        self.assertIn("laya_prune.py", post[0]["hooks"][0]["command"])
        self.assertIn("installed", msg)

    def test_idempotent(self):
        self.install("claude-code")
        self.install("claude-code")
        data = json.loads(self.SETTINGS.read_text())
        self.assertEqual(len(data["hooks"]["PostToolUse"]), 1)

    def test_uninstall_keeps_foreign(self):
        self.install("claude-code")
        data = json.loads(self.SETTINGS.read_text())
        data["hooks"]["PostToolUse"].append(
            {"matcher": "Edit", "hooks": [{"type": "command", "command": "other-tool"}]})
        self.SETTINGS.write_text(json.dumps(data))
        self.uninstall("claude-code")
        data = json.loads(self.SETTINGS.read_text())
        self.assertEqual(len(data["hooks"]["PostToolUse"]), 1)
        self.assertEqual(data["hooks"]["PostToolUse"][0]["matcher"], "Edit")

    def test_invalid_json_refuses(self):
        self.SETTINGS.write_text("{oops")
        with self.assertRaises(SystemExit):
            self.install("claude-code")


class TestMarkedBlocks(Sandbox):
    def test_copilot_roundtrip(self):
        target = Path(self.home) / ".copilot" / "copilot-instructions.md"
        self.install("copilot")
        self.install("copilot")  # idempotent
        self.assertEqual(target.read_text().count(es.MARK_BEGIN), 1)
        self.uninstall("copilot")
        self.assertNotIn(es.MARK_BEGIN, target.read_text())

    def test_codex_roundtrip(self):
        target = Path(self.home) / ".codex" / "AGENTS.md"
        self.install("codex")
        self.assertIn("lx", target.read_text())
        self.uninstall("codex")
        self.assertNotIn(es.MARK_BEGIN, target.read_text())

    def test_keeps_surrounding_content(self):
        target = Path(self.home) / ".codex" / "AGENTS.md"
        target.parent.mkdir(parents=True)
        target.write_text("# my notes\n")
        self.install("codex")
        self.assertIn("# my notes", target.read_text())
        self.uninstall("codex")
        self.assertIn("# my notes", target.read_text())


class TestVscode(Sandbox):
    SETTINGS = None

    def setUp(self):
        super().setUp()
        self.SETTINGS = Path(self.home) / ".config" / "Code" / "User" / "settings.json"
        self.SETTINGS.parent.mkdir(parents=True)
        self.SETTINGS.write_text(json.dumps({"editor.fontSize": 14}))

    def test_roundtrip(self):
        self.install("vscode")
        data = json.loads(self.SETTINGS.read_text())
        path = data["terminal"]["integrated"]["env"]["linux"]["PATH"]
        self.assertTrue(path.startswith(BIN + ":"))
        self.assertIn("${env:PATH}", path)
        self.assertEqual(data["editor.fontSize"], 14)
        self.uninstall("vscode")
        data = json.loads(self.SETTINGS.read_text())
        self.assertEqual(data["terminal"]["integrated"]["env"]["linux"]["PATH"], "${env:PATH}")

    def test_idempotent(self):
        self.install("vscode")
        self.install("vscode")
        data = json.loads(self.SETTINGS.read_text())
        path = data["terminal"]["integrated"]["env"]["linux"]["PATH"]
        self.assertEqual(path.count(BIN), 1)

    def test_placeholder_never_shredded(self):
        self.install("vscode")
        data = json.loads(self.SETTINGS.read_text())
        path = data["terminal"]["integrated"]["env"]["linux"]["PATH"]
        self.assertEqual(path.count("${env:PATH}"), 1)
        self.uninstall("vscode")
        data = json.loads(self.SETTINGS.read_text())
        self.assertEqual(data["terminal"]["integrated"]["env"]["linux"]["PATH"], "${env:PATH}")


class TestParseWith(unittest.TestCase):
    def test_all_and_csv(self):
        self.assertEqual(sorted(es.parse_with("all")), sorted(es.FEATURES))
        self.assertEqual(es.parse_with("vscode,codex"), ["vscode", "codex"])

    def test_unknown(self):
        with self.assertRaises(SystemExit):
            es.parse_with("vim")


if __name__ == "__main__":
    unittest.main()
