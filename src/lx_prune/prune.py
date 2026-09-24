"""Pipeline orchestration: dedupe -> collapse -> chunk -> force-keep -> score."""

import os
import time

from . import config
from .collapse import collapse_similar, dedupe_runs
from .model import score_chunks
from .modes import mode_adjust, resolve_mode
from .patterns import MUST_KEEP, SUMMARY_RE


def error_windows(lines, ctx):
    """Indices within ±ctx lines of any MUST_KEEP/SUMMARY line. Always kept."""
    hits = [i for i, l in enumerate(lines) if MUST_KEEP.search(l) or SUMMARY_RE.search(l)]
    if not hits:
        return set()
    keep, n = set(), len(lines)
    for h in hits:
        for i in range(max(0, h - ctx), min(n, h + ctx + 1)):
            keep.add(i)
    return keep


def rank_clusters(hits, ctx, n):
    """Group failure hits into clusters; first cluster = likely root cause.

    Returns (keep_indices, info). The first cluster gets the full ±ctx window;
    later (usually cascading) clusters get a shrunk ±max(2, ctx//3) window.
    Hits within `ctx` lines of each other belong to the same cluster.
    """
    hits = sorted(hits)
    clusters = [[hits[0]]]
    for h in hits[1:]:
        if h - clusters[-1][-1] <= ctx:
            clusters[-1].append(h)
        else:
            clusters.append([h])
    keep = set()
    for ci, cl in enumerate(clusters):
        w = ctx if ci == 0 else max(2, ctx // 3)
        for h in cl:
            for i in range(max(0, h - w), min(n, h + w + 1)):
                keep.add(i)
    return keep, {"clusters": len(clusters),
                  "root_size": len(clusters[0]),
                  "cascade": sum(len(c) for c in clusters[1:])}


def ranked_windows(lines, ctx):
    """error_windows + cluster ranking. Returns (keep_set, info)."""
    hits = [i for i, l in enumerate(lines) if MUST_KEEP.search(l) or SUMMARY_RE.search(l)]
    if not hits:
        return set(), {"clusters": 0, "root_size": 0, "cascade": 0}
    return rank_clusters(hits, ctx, len(lines))


def prune(text):
    """Return (pruned_text, info). info feeds the metrics."""
    t0 = time.time()
    info = {"chunks": 0, "forced": 0, "scored": 0, "dropped_chunks": 0, "collapsed": 0,
            "model": "skipped"}
    cmd_label = os.getenv("LAYA_PRUNE_CMD", "")
    mode, rc = resolve_mode(cmd_label)
    info["mode"] = mode
    if rc:
        info["rc"] = rc
    _, collapse_mult, ctx = mode_adjust(mode)
    orig_lines = text.splitlines()
    if len(orig_lines) <= config.MIN_LINES:
        info["ms"] = int((time.time() - t0) * 1000)
        return text, info

    lines = dedupe_runs(orig_lines)
    if os.getenv("LAYA_PRUNE_NO_COLLAPSE") != "1":
        lines, info["collapsed"] = collapse_similar(lines, min_count=config.COLLAPSE_MIN * collapse_mult)

    if len(lines) <= config.MIN_LINES:  # already short enough: no model needed
        out = lines
    else:
        # Error neighborhoods, cluster-ranked: the first failure cluster is the
        # likely root cause (full window); later clusters are usually cascades
        # (shrunk window). Setup lines explaining an error survive even when
        # the error itself sits in a different chunk.
        protected, cluster_info = ranked_windows(lines, ctx)
        info["clusters"] = cluster_info["clusters"]
        if cluster_info["clusters"]:
            first = min(i for i, l in enumerate(lines)
                        if MUST_KEEP.search(l) or SUMMARY_RE.search(l))
            info["root_cause"] = lines[first].strip()[:120]
        body = lines[config.HEAD: len(lines) - config.TAIL]
        # Balanced: small chunks for accuracy, capped so huge logs don't make
        # giant context-free blobs. Extra chunks run in batches inside score_chunks.
        size = max(config.CHUNK, -(-len(body) // config.MAX_CHUNKS))
        size = min(size, config.CHUNK_MAX)
        chunks = ["\n".join(body[i: i + size]) for i in range(0, len(body), size)]
        forced = []
        for n, c in enumerate(chunks):
            span = set(range(config.HEAD + n * size, config.HEAD + n * size + len(c.splitlines())))
            forced.append(bool(MUST_KEEP.search(c)) or bool(SUMMARY_RE.search(c))
                          or bool(span & protected))
        to_score = [i for i, f in enumerate(forced) if not f]
        verdicts = dict(zip(to_score, score_chunks([chunks[i] for i in to_score], info,
                                                   cmd_label=cmd_label)))

        out, dropped = list(lines[:config.HEAD]), 0
        for i, chunk in enumerate(chunks):
            if forced[i] or verdicts.get(i, True):
                if dropped:
                    out.append(f"[... {dropped} lines of routine output omitted ...]")
                    dropped = 0
                out.append(chunk)
            else:
                dropped += chunk.count("\n") + 1
                info["dropped_chunks"] += 1
        if dropped:
            out.append(f"[... {dropped} lines of routine output omitted ...]")
        out.extend(lines[len(lines) - config.TAIL:])
        info.update(chunks=len(chunks), forced=sum(forced), scored=len(to_score))

    info["ms"] = int((time.time() - t0) * 1000)
    if out == orig_lines:
        return text, info  # nothing removed: hand back the original byte-for-byte
    pruned = "\n".join(out) + ("\n" if text.endswith("\n") else "")
    return (pruned if len(pruned) < len(text) else text), info


def prune_text(text, cmd_label=None):
    """Importable entry for the `lx` unified wrapper.

    Same as prune() but takes an explicit command label instead of reading
    LAYA_PRUNE_CMD from the environment. Returns (pruned_text, info).
    """
    if cmd_label is not None:
        os.environ["LAYA_PRUNE_CMD"] = cmd_label
    return prune(text)
