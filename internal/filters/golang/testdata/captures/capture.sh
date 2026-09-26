#!/bin/bash
# Records one real go command output as a fixture for this package's tests.
#
#   SP=/path/to/scratch ./capture.sh <name> <cwd> <description> -- <argv...>
#
# SP must hold corpus/gopath (a module cache, so nothing is fetched) and
# golangcap/gocache. Paths are sanitized as in testdata/corpus: the scratch
# prefix becomes /home/user/src/ (repos) or /home/user/, the user name "user".
set -u
: "${SP:?set SP to the scratch directory}"
OUT=$(cd "$(dirname "$0")" && pwd)/go
mkdir -p "$OUT"
name=$1; cwd=$2; desc=$3; shift 4
cd "$cwd" || exit 1
export GOCACHE=$SP/golangcap/gocache GOPATH=$SP/corpus/gopath GOMODCACHE=$SP/corpus/gopath/pkg/mod GOFLAGS= GOPROXY=off GOTOOLCHAIN=local
"$@" > "$SP/golangcap/raw.txt" 2>&1
code=$?
SP="$SP" OUT="$OUT" python3 - "$name" "$cwd" "$desc" "$code" "$@" <<'PY'
import json, os, sys
SP, OUT = os.environ['SP'], os.environ['OUT']
name, cwd, desc, code = sys.argv[1:5]
argv = sys.argv[5:]
def san(s):
    s = s.replace(SP + '/corpus/repos/', '/home/user/src/').replace(SP + '/golangcap/', '/home/user/src/').replace(SP + '/corpus/', '/home/user/')
    home, user = os.path.expanduser('~'), os.environ.get('USER', 'user')
    s = s.replace(SP, '/home/user/tmp').replace(home, '/home/user').replace(user, 'user')
    return s
raw = open(SP + '/golangcap/raw.txt', encoding='utf-8', errors='replace').read()
open(f'{OUT}/{name}.txt', 'w').write(san(raw))
json.dump({'argv': argv, 'shell': ' '.join(argv), 'repo': '', 'exit_code': int(code), 'description': desc, 'cwd': san(cwd)},
          open(f'{OUT}/{name}.meta.json', 'w'), indent=2)
print(name, 'exit', code, 'lines', raw.count('\n'))
PY
