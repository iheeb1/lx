import argparse
import fcntl
import json
import logging
import logging.handlers
import os
import platform
import signal
import socket
import stat
import sys
import threading
import time
import traceback

VERSION = 1
EXIT_BUSY = 3
MODEL = "convaiinnovations/laya"
SUBFOLDER = "typed-decisions"

BATCH = 16
MAX_CHARS = 2400
MAX_TASK = 400
MAX_ITEMS = 4096
MAX_LINE = 16 << 20
MAX_CONNS = 32
IDLE_S = 60.0
DEADLINE_MS = 10000
MS_PER_ITEM = 175.0

GENERIC = (
    "This is a slice of shell command output seen by a coding agent. "
    "Is it needed to debug problems or verify the result, or is it routine noise "
    "such as progress bars, repeated log lines, or download logs?",
    "needed: failures, results, summaries, or facts the agent must see",
    "noise: progress output, repetition, routine logs, no new information",
)

TEMPLATES = {
    "log": GENERIC,
    "lines": GENERIC,
    "diff": (
        "This is a slice of a git diff seen by a coding agent. "
        "Hunk headers, added/removed lines, and file names are needed. "
        "Unchanged context lines and repeated index hashes are noise.",
        "needed: file paths, hunk headers (@@), +/- lines, errors",
        "noise: unchanged context, index hashes, repeated mode lines",
    ),
    "listing": (
        "This is a slice of a file listing or status output (ls, git status, find). "
        "Every line is usually a distinct fact the agent needs. "
        "Only drop exact duplicates or pure progress noise.",
        "needed: file names, status flags, paths (usually keep all)",
        "noise: exact duplicate lines or download-style progress only",
    ),
}

THRESHOLDS = {"log": 0.50, "lines": 0.65, "diff": 0.65, "listing": 0.80}

UNJUDGED = {"keep": True, "confidence": 0.0}

log = logging.getLogger("lx-laya")


class BadRequest(Exception):
    pass


def clean_task(task):
    return " ".join(task.split())[:MAX_TASK].rstrip(" .")


def questions(family, task=""):
    instructions, needed, noise = TEMPLATES[family]
    task = clean_task(task)
    if task:
        instructions += (" The coding agent is working on: " + task + ". "
                         "Output that bears on this task is needed.")
    return {"keep": {"type": "choice", "instructions": instructions,
                     "criteria": {"A": needed, "B": noise}}}


def model_input(text):
    if len(text) <= MAX_CHARS:
        return text
    head = int(MAX_CHARS * 0.65)
    return text[:head] + "\n[... chunk truncated for scoring ...]\n" + text[-(MAX_CHARS - head):]


def _prob(v):
    if isinstance(v, bool) or not isinstance(v, (int, float)) or v != v:
        return None
    return min(1.0, max(0.0, float(v)))


def calibrated(answer):
    conf = _prob(answer.get("answer_confidence"))
    if conf is None:
        probs = answer.get("probabilities")
        if isinstance(probs, dict):
            ps = [p for p in (_prob(probs.get("A")), _prob(probs.get("B"))) if p is not None]
            conf = max(ps) if ps else None
    if not conf:
        conf = _prob(answer.get("confidence")) or 0.0
    return conf


def verdict(result, threshold):
    try:
        answer = result["answers"]["keep"]
        choice = answer.get("choice")
        conf = calibrated(answer)
    except (KeyError, TypeError, AttributeError):
        return dict(UNJUDGED)
    return {"keep": choice != "B" or conf < threshold, "confidence": round(conf, 4)}


def _number(req, name, lo, hi, default):
    v = req.get(name)
    if v is None:
        return default
    if isinstance(v, bool) or not isinstance(v, (int, float)) or v != v:
        raise BadRequest(name + " must be a number")
    return min(hi, max(lo, v))


def rss():
    try:
        with open("/proc/self/statm") as f:
            return int(f.read().split()[1]) * os.sysconf("SC_PAGE_SIZE"), False
    except (OSError, ValueError, IndexError):
        pass
    try:
        import resource
        peak = resource.getrusage(resource.RUSAGE_SELF).ru_maxrss
        return (peak if sys.platform == "darwin" else peak * 1024), True
    except (ImportError, OSError):
        return 0, False


class LayaModel:
    name = MODEL + "/" + SUBFOLDER

    def __init__(self):
        self.agent = None
        self.version = ""

    def load(self):
        import laya
        self.version = getattr(laya, "__version__", "")
        self.agent = laya.load(MODEL, subfolder=SUBFOLDER)

    def predict(self, texts, qs):
        return self.agent.predict_batch([{"output": t} for t in texts], qs)


class Daemon:
    def __init__(self, model, clock=time.monotonic):
        self.model = model
        self.clock = clock
        self.started = clock()
        self.loaded = False
        self.load_error = ""
        self.load_ms = 0
        self.lock = threading.Lock()
        self.ms_per_item = MS_PER_ITEM
        self.requests = 0
        self.items = 0
        self.last = None
        self.stopping = threading.Event()
        self.shutdown_requested = False

    def load(self):
        t = self.clock()
        try:
            self.model.load()
            self.model.predict(["lx: warm-up"], questions("lines"))
        except Exception as e:
            name = type(e).__name__
            self.load_error = "model failed to load: %s: %s" % (name, str(e)[:300])
            if "EntryNotFound" in name or "Offline" in name:
                self.load_error += " (the daemon runs offline: lx laya setup downloads the model)"
            log.exception("model failed to load")
            self.stopping.set()
            return False
        self.load_ms = int((self.clock() - t) * 1000)
        self.loaded = True
        log.info("model %s loaded in %d ms", self.model.name, self.load_ms)
        return True

    def handle_line(self, line):
        t0 = self.clock()
        try:
            req = json.loads(line)
        except (ValueError, RecursionError) as e:
            return {"error": "bad json: " + str(e)[:200]}
        if not isinstance(req, dict):
            return {"error": "a request is a JSON object"}
        op = req.get("op")
        try:
            if op == "ping":
                return self.ping()
            if op == "status":
                return self.status()
            if op == "judge":
                return self.judge(req, t0)
            if op == "shutdown":
                self.shutdown_requested = True
                return {"ok": True}
        except BadRequest as e:
            return {"error": str(e)}
        except Exception as e:
            _log_error(op + " failed", e)
            return {"error": "%s: %s" % (type(e).__name__, str(e)[:300])}
        return {"error": "unknown op " + json.dumps(op)[:80]}

    def ping(self):
        r = {"ok": True, "model": self.model.name, "loaded": self.loaded,
             "version": VERSION, "pid": os.getpid()}
        if self.load_error:
            r["error"] = self.load_error
        return r

    def status(self):
        r = self.ping()
        mem, peak = rss()
        r.update(uptime_s=round(self.clock() - self.started, 1), load_ms=self.load_ms,
                 rss_bytes=mem, rss_peak=peak, requests=self.requests, items=self.items,
                 ms_per_item=round(self.ms_per_item, 1), python=platform.python_version(),
                 laya=getattr(self.model, "version", ""))
        last = self.last
        if last:
            last = dict(last)
            last["ago_s"] = round(self.clock() - last.pop("at"), 1)
            r["last"] = last
        return r

    def judge(self, req, t0):
        family = req.get("family")
        if not isinstance(family, str) or family not in TEMPLATES:
            raise BadRequest("family must be one of " + ", ".join(sorted(TEMPLATES)))
        task = req.get("task")
        if task is None:
            task = ""
        if not isinstance(task, str):
            raise BadRequest("task must be a string")
        items = req.get("items")
        if not isinstance(items, list):
            raise BadRequest("items must be a list")
        if len(items) > MAX_ITEMS:
            raise BadRequest("at most %d items per request" % MAX_ITEMS)
        texts = []
        for i, it in enumerate(items):
            text = it.get("text") if isinstance(it, dict) else None
            if not isinstance(text, str):
                raise BadRequest("items[%d].text must be a string" % i)
            texts.append(model_input(text))
        deadline_ms = _number(req, "deadline_ms", 0, 600000, DEADLINE_MS)
        threshold = _number(req, "min_conf", 0.30, 1.0, THRESHOLDS[family])
        if not self.loaded:
            raise BadRequest(self.load_error or "loading the model")

        deadline = t0 + deadline_ms / 1000.0
        verdicts, model_ms = [], 0.0
        if texts and self.lock.acquire(timeout=max(0.0, deadline - self.clock())):
            try:
                qs = questions(family, task)
                while len(verdicts) < len(texts):
                    left_ms = (deadline - self.clock()) * 1000
                    n = min(BATCH, len(texts) - len(verdicts), int(left_ms // max(self.ms_per_item, 1.0)))
                    if n < 1:
                        break
                    i = len(verdicts)
                    t = self.clock()
                    results = list(self.model.predict(texts[i:i + n], qs) or [])[:n]
                    dt = (self.clock() - t) * 1000
                    model_ms += dt
                    self.ms_per_item = 0.7 * self.ms_per_item + 0.3 * dt / n
                    results += [None] * (n - len(results))
                    verdicts.extend(verdict(r, threshold) for r in results)
            finally:
                self.lock.release()
        judged = len(verdicts)
        verdicts.extend(dict(UNJUDGED) for _ in range(len(texts) - judged))
        total_ms = int((self.clock() - t0) * 1000)
        self.requests += 1
        self.items += len(texts)
        self.last = {"items": len(texts), "judged": judged, "model_ms": int(model_ms),
                     "total_ms": total_ms, "at": self.clock()}
        log.info("judge %s: %d/%d items in %d ms (model %d ms)",
                 family, judged, len(texts), total_ms, int(model_ms))
        return {"verdicts": verdicts, "model_ms": int(model_ms)}


def _log_error(what, e):
    log.error("%s: %s\n%s", what, type(e).__name__, "".join(traceback.format_tb(e.__traceback__)).rstrip())


class AlreadyRunning(Exception):
    pass


def _alive(path):
    s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    s.settimeout(1.0)
    try:
        s.connect(path)
        return True
    except (ConnectionRefusedError, FileNotFoundError):
        return False
    except OSError:
        return True
    finally:
        s.close()


class Server:
    def __init__(self, daemon, path, pid_path=None):
        self.daemon = daemon
        self.path = path
        self.pid_path = pid_path
        self.sock = None
        self.ino = None
        self.lock = None
        self.slots = threading.BoundedSemaphore(MAX_CONNS)

    def bind(self):
        d = os.path.dirname(self.path) or "."
        if not os.path.isdir(d):
            os.makedirs(d, mode=0o700)
            os.chmod(d, 0o700)
        st = os.lstat(d)
        if not stat.S_ISDIR(st.st_mode) or st.st_uid != os.getuid():
            raise OSError("%s is not a directory of yours" % d)
        if st.st_mode & 0o022:
            raise OSError("others can write to %s" % d)
        self._lock(os.path.splitext(self.path)[0] + ".lock")
        try:
            self._bind()
        except BaseException:
            self._unlock()
            raise

    def _bind(self):
        if os.path.lexists(self.path):
            if not stat.S_ISSOCK(os.lstat(self.path).st_mode):
                raise OSError("%s exists and is not a socket; not touching it" % self.path)
            if _alive(self.path):
                raise AlreadyRunning(self.path)
            os.unlink(self.path)
            log.info("removed a stale socket %s", self.path)
        sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        old = os.umask(0o177)
        try:
            sock.bind(self.path)
        except OSError:
            sock.close()
            raise
        finally:
            os.umask(old)
        os.chmod(self.path, 0o600)
        sock.listen(64)
        sock.settimeout(0.25)
        self.sock = sock
        self.ino = os.stat(self.path).st_ino
        if self.pid_path:
            tmp = self.pid_path + ".tmp"
            with open(tmp, "w") as f:
                f.write("%d\n" % os.getpid())
            os.chmod(tmp, 0o600)
            os.replace(tmp, self.pid_path)

    def _lock(self, path):
        fd = os.open(path, os.O_RDWR | os.O_CREAT, 0o600)
        try:
            fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except OSError:
            os.close(fd)
            raise AlreadyRunning(path)
        self.lock = fd

    def _unlock(self):
        if self.lock is not None:
            os.close(self.lock)
            self.lock = None

    def serve(self):
        while not self.daemon.stopping.is_set():
            try:
                conn, _ = self.sock.accept()
            except socket.timeout:
                continue
            except OSError:
                if self.daemon.stopping.is_set():
                    break
                time.sleep(0.05)
                continue
            if not self.slots.acquire(blocking=False):
                self._reply(conn, {"error": "busy"})
                conn.close()
                continue
            threading.Thread(target=self._conn, args=(conn,), daemon=True).start()

    def _reply(self, conn, resp):
        try:
            conn.sendall(json.dumps(resp).encode() + b"\n")
            return True
        except OSError:
            return False

    def _conn(self, conn):
        try:
            conn.settimeout(IDLE_S)
            f = conn.makefile("rb")
            while not self.daemon.stopping.is_set():
                try:
                    line = f.readline(MAX_LINE + 1)
                except OSError:
                    return
                if not line:
                    return
                if len(line) > MAX_LINE and not line.endswith(b"\n"):
                    self._reply(conn, {"error": "request over %d bytes" % MAX_LINE})
                    return
                if not line.strip():
                    continue
                ok = self._reply(conn, self.daemon.handle_line(line))
                if self.daemon.shutdown_requested:
                    self.daemon.stopping.set()
                if not ok:
                    return
        except Exception as e:
            _log_error("connection failed", e)
        finally:
            conn.close()
            self.slots.release()

    def close(self):
        if self.sock is not None:
            self.sock.close()
        try:
            if os.stat(self.path).st_ino == self.ino:
                os.unlink(self.path)
        except OSError:
            pass
        if self.pid_path:
            try:
                with open(self.pid_path) as f:
                    mine = f.read().strip() == str(os.getpid())
                if mine:
                    os.unlink(self.pid_path)
            except OSError:
                pass
        self._unlock()


class _LogFile(logging.handlers.RotatingFileHandler):
    def doRollover(self):
        super().doRollover()
        _redirect(self.stream)


def _redirect(stream):
    try:
        sys.stdout.flush()
        sys.stderr.flush()
        os.dup2(stream.fileno(), 1)
        os.dup2(stream.fileno(), 2)
    except (OSError, ValueError, AttributeError):
        pass


def _setup_logging(path):
    h = None
    if path:
        try:
            os.makedirs(os.path.dirname(path) or ".", mode=0o700, exist_ok=True)
            h = _LogFile(path, maxBytes=1 << 20, backupCount=2)
            _redirect(h.stream)
        except OSError as e:
            print("lx laya: cannot write %s: %s" % (path, e), file=sys.stderr)
    if h is None:
        h = logging.StreamHandler(sys.stderr)
    h.setFormatter(logging.Formatter("%(asctime)s %(levelname)s %(message)s"))
    root = logging.getLogger()
    root.addHandler(h)
    root.setLevel(logging.INFO)
    logging.captureWarnings(True)


def _cache_dir():
    home = os.path.expanduser("~")
    if sys.platform == "darwin":
        return os.path.join(home, "Library", "Caches")
    xdg = os.environ.get("XDG_CACHE_HOME", "")
    return xdg if os.path.isabs(xdg) else os.path.join(home, ".cache")


def main(argv=None, model=None):
    lx = os.path.join(_cache_dir(), "lx")
    p = argparse.ArgumentParser(prog="lx_laya.py")
    p.add_argument("--socket", default=os.path.join(lx, "laya.sock"))
    p.add_argument("--pid", default=os.path.join(lx, "laya.pid"))
    p.add_argument("--log", default="")
    p.add_argument("--fetch", action="store_true", help="download the model and exit (uses the network)")
    args = p.parse_args(argv)
    os.umask(0o077)
    os.environ["HF_HUB_DISABLE_TELEMETRY"] = "1"
    model = model or LayaModel()

    if args.fetch:
        logging.basicConfig(level=logging.WARNING)
        try:
            model.load()
        except Exception as e:
            print("lx laya: cannot download the model: %s: %s" % (type(e).__name__, e), file=sys.stderr)
            return 1
        print("lx laya: model %s is ready" % model.name)
        return 0

    os.environ["HF_HUB_OFFLINE"] = "1"
    os.environ["TRANSFORMERS_OFFLINE"] = "1"
    _setup_logging(args.log)
    daemon = Daemon(model)
    server = Server(daemon, args.socket, args.pid)
    try:
        server.bind()
    except AlreadyRunning:
        log.error("%s is in use: another daemon is running", args.socket)
        return EXIT_BUSY
    except OSError as e:
        log.error("cannot listen on %s: %s", args.socket, e)
        return 2

    def stop(signum, frame):
        daemon.stopping.set()

    for sig in (signal.SIGTERM, signal.SIGINT, signal.SIGHUP):
        signal.signal(sig, stop)
    log.info("pid %d listening on %s (python %s)", os.getpid(), args.socket, platform.python_version())
    threading.Thread(target=daemon.load, daemon=True).start()
    try:
        server.serve()
    finally:
        server.close()
        log.info("stopped")
    return 1 if daemon.load_error else 0


if __name__ == "__main__":
    code = main()
    logging.shutdown()
    sys.stdout.flush()
    os._exit(code)
