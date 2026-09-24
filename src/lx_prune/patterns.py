"""Keep/drop regexes. MUST_KEEP and SUMMARY_RE lines are never model-scored."""

import re

MUST_KEEP = re.compile(
    r"\berror\b|\bfailed?\b|\bfailure\b|exception|traceback|\bfatal\b|panic|denied|"
    r"not found|cannot|\bwarning\b|warn:|assert|segfault|timeout|exit (code|status)|"
    r"^\s*at .*\(.*:\d+|File \".*\", line \d+|\bE\s{2,}|npm ERR|Killed",
    re.I,
)

# Result summaries are what an agent checks after a green run ("did it pass?").
# Kept like MUST_KEEP so verify-mode drops noise but never the verdict.
SUMMARY_RE = re.compile(
    r"\b\d+\s*(passed|failed|failed|ok|tests?|assertions?|errors?)\b|"
    r"BUILD (SUCCESS|FAILURE)|coverage|Time: \d|tests? (passed|failed)|OK \(|"
    r"Result: |All tests (passed|green)",
    re.I,
)

# Log severity: ERROR/WARN lines are already MUST_KEEP-exempt from collapse.
# INFO/DEBUG collapse normally, except in debug mode (COLLAPSE_MIN doubled).
LEVEL_RE = re.compile(r"\b(ERROR|ERR|FATAL|CRITICAL|FAIL(?:ED|URE)?|WARN(?:ING)?|"
                      r"INFO|DEBUG|TRACE)\b", re.I)

# Mix of safety + savings: noisy build/log commands get a lower bar to DROP
# (more savings), file-listing / status commands get a higher bar
# (more safety, they are usually all-unique and needed).
NOISY_RE = re.compile(r"npm|pip|uv|yarn|pnpm|docker|kubectl|terraform|aws|"
                      r"build|test|pytest|pest|phpunit|paratest|phpstan|psalm|ecs|pint|"
                      r"jest|vitest|tsc|eslint|ruff|mypy|prettier|cargo|go test|mvn|gradle|"
                      r"composer|curl|wget|apt|dotnet|prisma|playwright|"
                      r"git\s+(log|diff|blame|show)\b|ps\b|tree\b", re.I)
KEEPY_RE = re.compile(r"^\s*(ls|tree|cat|rg|grep|find|diff|show|git\s+"
                      r"(status|branch|worktree|stash|show\b.*--stat))\b", re.I)
# Interactive / never-ending commands must bypass the pruner entirely.
# Includes follow-mode variants: buffering a stream that never ends shows
# a black screen (the wrapper only prints when the command exits).
# Deliberately explicit (no generic "-f" match: `grep -f`, `rm -f` exit
# normally and must still be pruned).
INTERACTIVE_RE = re.compile(r"^\s*(watch\b|tail\s+(-f|--follow)\b|tailf\b|less\b|more\b|"
                            r"top\b|htop\b|ssh\b|npm\s+run\s+dev|symfony\s+serve|php\s+-S|"
                            r"docker\s+logs\b.*?(?:-f|--follow)\b|"
                            r"kubectl\s+logs\b.*?(?:-f|--follow)\b|"
                            r"journalctl\b.*?(?:-f|--follow)\b)", re.I)
# Commands RTK owns deterministically (rtk-first routing suggestion, no double-exec).
RTK_OWNED_RE = re.compile(r"^\s*(ls\b|tree\b|git\s+(status|log|diff|show|branch|stash|worktree)|"
                          r"grep\b|rg\b|find\b|docker\b|kubectl\b|npm\b|npx\b|"
                          r"pytest\b|pest\b|phpunit\b|ps\b|curl\b|diff\b)", re.I)
