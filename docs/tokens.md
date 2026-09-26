# Counting tokens without a tokenizer

lx needs token counts on every run (for its budget, its "never worse" gate and
`lx gain`), but it ships as one small static binary with no vocabulary files and
no network access. The usual shortcut, `bytes ÷ 4`, is badly wrong on developer
output: long runs of punctuation, indentation, hex, paths and numbers tokenize
very differently from English prose.

## How the estimator works

BPE tokenizers such as `cl100k_base` and `o200k_base` work in two steps:

1. A **pre-tokenizer** regex chops the text into pieces: words (with an optional
   leading space or punctuation character), groups of up to three digits,
   punctuation runs, and whitespace runs.
2. **BPE merges** run inside each piece. A piece never merges with its neighbour.

Step 1 is a regular expression, so lx reproduces it exactly
(`internal/tokens/tokens.go`, `piece()`). Step 2 needs the vocabulary, so lx
replaces it with a cost curve per piece class, fitted against real counts:

| piece | cost |
|---|---|
| digits (1–3) | 1 |
| whitespace run | 1 |
| ASCII word ≤ 4 letters | 1 |
| ASCII word 5–8 letters | 1 + 0.12 per letter past 4 |
| longer ASCII word | 1.5 + 0.2 per letter past 8 |
| ALL-CAPS word | 0.55 per letter |
| Capitalized word | ×1.12 |
| word led by punctuation (`.foo`, `/bar`) | +0.3 |
| non-ASCII word (Arabic, CJK, accented…) | 0.9 per letter |
| punctuation run | 1, +0.5 per character past 3; a run of one repeated ASCII character (`=====`, `-----`, `.....`) costs 1, because BPE vocabularies merge those |
| non-ASCII symbol (`✓ ❯ ⎯ │`) | 1 (2-byte), 1.6 (3-byte), 2.5 (4-byte) |

## Accuracy

Measured on the 109 captures in `testdata/corpus` that are at least 200
characters long, against exact `tiktoken` counts (`bench/tiktoken_counts.py`):

| method | mean error vs cl100k | mean error vs o200k | worst case |
|---|---|---|---|
| bytes ÷ 4 | 18.6% | 19.0% | 52% |
| bytes ÷ 3 | 20.3% | 19.7% | 102% |
| **lx estimator** | **6.4%** | **6.0%** | 24% |

![estimator accuracy](img/estimator.svg)

Savings percentages are even more accurate than absolute counts. The same
estimator measures both sides of the ratio, so most of its bias cancels out.

The benchmark numbers in the README use **exact** o200k counts, not this
estimator.

## Reproducing

```sh
python3 -m venv .venv && .venv/bin/pip install tiktoken
make bench                                # estimator counts → bench/out/results.json
.venv/bin/python bench/tiktoken_counts.py # exact counts → bench/out/tokstats.json
make bench                                # re-run to score the estimator
```

`go test ./internal/tokens -run Calibration` checks the estimator against those
counts (set `LX_TOKSTATS=bench/out/tokstats.json LX_CORPUS=testdata/corpus`).
It fails if mean error rises above 8%.

Claude's tokenizer is not public. Treat these counts as a close proxy, not
as Anthropic token counts.
