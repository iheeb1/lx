"""Unit tests: interactive bypass regex (never buffer a live stream)."""

import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent.parent / "src"))

from lx_prune.patterns import INTERACTIVE_RE


class TestBypass(unittest.TestCase):
    BYPASS = [
        "watch -n1 kubectl get pods",
        "tail -f var/log/dev.log",
        "tail --follow var/log/dev.log",
        "tailf var/log/dev.log",
        "less file.txt",
        "top",
        "ssh user@host",
        "docker logs -f php-fpm",
        "docker logs --follow --tail 50 php-fpm",
        "kubectl logs -f deploy/api",
        "journalctl -f -u php",
        "journalctl --follow",
    ]
    KEEP = [
        "tail -n 50 var/log/dev.log",     # exits: must still prune
        "grep -f patterns.txt app.log",   # -f reads a file: exits
        "rm -f old.log",
        "docker logs php-fpm",            # bounded: prune it
        "kubectl logs deploy/api --tail 50",
        "git log --oneline",
    ]

    def test_bypassed(self):
        for cmd in self.BYPASS:
            self.assertTrue(INTERACTIVE_RE.search(cmd), f"should bypass: {cmd}")

    def test_not_bypassed(self):
        for cmd in self.KEEP:
            self.assertFalse(INTERACTIVE_RE.search(cmd), f"should NOT bypass: {cmd}")


if __name__ == "__main__":
    unittest.main()
