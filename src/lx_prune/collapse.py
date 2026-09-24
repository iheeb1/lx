"""Model-free shrinking: exact-duplicate folding + similar-line collapse.

Runs before the model. Lines matching MUST_KEEP, blank lines and
pure-punctuation lines are never touched by either pass.
"""

import re
from collections import defaultdict

from . import config
from .patterns import MUST_KEEP

_NORM = [
    (re.compile(r"\b[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}\b"), "<uuid>"),
    (re.compile(r"\b[0-9a-f]{7,40}\b"), "<sha>"),  # git short/full hashes before generic hex
    (re.compile(r"\b[0-9a-fA-F]{8,}\b"), "<hex>"),
    (re.compile(r"\bv?\d+\.\d+(?:\.\d+)*(?:[-+][\w.]+)?\b"), "<ver>"),
    (re.compile(r"\b\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}(?::\d{2})?\b"), "<ts>"),
    (re.compile(r"index <sha>\.\.<sha>"), "index <sha>..<sha>"),
    (re.compile(r"\d+"), "#"),
]


def _template(line):
    for rx, rep in _NORM:
        line = rx.sub(rep, line)
    # paths differing only in the tail (pkg-123, chunk 17/200) share a template
    line = re.sub(r"([A-Za-z_-]+)#([A-Za-z_-]*)", r"\1#\2", line)
    return line.strip()


def dedupe_runs(lines):
    """Fold runs of >=3 identical consecutive lines into one + marker."""
    out, i = [], 0
    while i < len(lines):
        j = i
        while j + 1 < len(lines) and lines[j + 1] == lines[i]:
            j += 1
        n = j - i + 1
        out.append(lines[i])
        if n >= 3:
            out.append(f"[... previous line repeated {n - 1} more times ...]")
        else:
            out.extend(lines[i + 1: j + 1])
        i = j + 1
    return out


def collapse_similar(lines, min_count=None):
    """Collapse lines differing only in numbers/hashes ("Downloading chunk 17").

    Groups with >= min_count members keep the first 2 + last 1 plus a marker.
    """
    if min_count is None:
        min_count = config.COLLAPSE_MIN
    groups = defaultdict(list)
    for i, l in enumerate(lines):
        if re.search(r"\w", l) and not MUST_KEEP.search(l):
            groups[_template(l)].append(i)
    drop, marker = set(), {}
    for ids in groups.values():
        if len(ids) >= min_count:
            gone = ids[2:-1]
            drop.update(gone)
            marker[gone[0]] = (len(gone), lines[ids[0]].strip()[:80])
    out = []
    for i, l in enumerate(lines):
        if i in marker:
            out.append(f"[... {marker[i][0]} similar lines omitted, e.g. {marker[i][1]!r} ...]")
        if i not in drop:
            out.append(l)
    return out, len(drop)
