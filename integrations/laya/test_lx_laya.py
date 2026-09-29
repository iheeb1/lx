import json
import os
import shutil
import socket
import stat
import subprocess
import sys
import tempfile
import threading
import time
import unittest

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import lx_laya  # noqa: E402

HERE = os.path.dirname(os.path.abspath(__file__))


def setUpModule():
    lx_laya.log.disabled = True


def tearDownModule():
    lx_laya.log.disabled = False


def answer(choice, conf):
    return {"answers": {"keep": {"type": "choice", "choice": choice,
                                 "probabilities": {"A": 1 - conf if choice == "B" else conf,
                                                   "B": conf if choice == "B" else 1 - conf},
                                 "answer_confidence": conf, "confidence": 0.01}}}


class FakeModel:
    name = "fake/model"
    version = "0"

    def __init__(self, clock=None, ms_per_item=0, fail=None):
        self.batches = []
        self.questions = []
        self.clock = clock
        self.ms_per_item = ms_per_item
        self.fail = fail
        self.loads = 0

    def load(self):
        self.loads += 1
        if self.fail == "load":
            raise RuntimeError("no weights")

    def predict(self, texts, qs):
        if self.fail == "predict":
            raise RuntimeError("boom")
        self.batches.append(len(texts))
        self.questions.append(qs)
        if self.clock:
            self.clock.t += self.ms_per_item * len(texts) / 1000.0
        return [answer("B", 0.9) if "noise" in t else answer("A", 0.8) for t in texts]


class Clock:
    def __init__(self):
        self.t = 100.0

    def __call__(self):
        return self.t


def loaded(model=None, clock=None):
    d = lx_laya.Daemon(model or FakeModel(), clock=clock or time.monotonic)
    assert d.load()
    return d


def judge(d, **req):
    req.setdefault("op", "judge")
    return d.handle_line(json.dumps(req).encode())


class TestQuestions(unittest.TestCase):
    def test_families(self):
        for fam in ("log", "lines", "diff", "listing"):
            q = lx_laya.questions(fam)["keep"]
            self.assertEqual(q["type"], "choice")
            self.assertEqual(set(q["criteria"]), {"A", "B"})
            self.assertTrue(q["criteria"]["A"].startswith("needed:"))
            self.assertNotIn("working on", q["instructions"])
        self.assertIn("git diff", lx_laya.questions("diff")["keep"]["instructions"])
        self.assertIn("file listing", lx_laya.questions("listing")["keep"]["instructions"])

    def test_task(self):
        q = lx_laya.questions("log", "  fix the\n flaky TestParse.  ")["keep"]["instructions"]
        self.assertIn("The coding agent is working on: fix the flaky TestParse. ", q)
        long = lx_laya.questions("log", "x" * 5000)["keep"]["instructions"]
        self.assertLess(len(long), len(lx_laya.GENERIC[0]) + lx_laya.MAX_TASK + 100)
        self.assertEqual(lx_laya.questions("log", " \n ")["keep"]["instructions"], lx_laya.GENERIC[0])

    def test_model_input(self):
        self.assertEqual(lx_laya.model_input("short"), "short")
        text = "HEAD" + "x" * 10000 + "TAIL"
        got = lx_laya.model_input(text)
        self.assertTrue(got.startswith("HEAD") and got.endswith("TAIL"))
        self.assertIn("[... chunk truncated for scoring ...]", got)
        self.assertLess(len(got), lx_laya.MAX_CHARS + 60)


class TestCalibration(unittest.TestCase):
    def test_answer_confidence_first(self):
        a = {"choice": "B", "probabilities": {"A": 0.3, "B": 0.7}, "answer_confidence": 0.66, "confidence": 0.01}
        self.assertEqual(lx_laya.calibrated(a), 0.66)

    def test_probabilities_fallback(self):
        self.assertEqual(lx_laya.calibrated({"probabilities": {"A": 0.3, "B": 0.7}, "confidence": 0.01}), 0.7)
        self.assertEqual(lx_laya.calibrated({"probabilities": {"A": "x", "B": 0.6}}), 0.6)

    def test_raw_last(self):
        self.assertEqual(lx_laya.calibrated({"confidence": 0.12}), 0.12)
        self.assertEqual(lx_laya.calibrated({}), 0.0)
        self.assertEqual(lx_laya.calibrated({"answer_confidence": float("nan"), "probabilities": None}), 0.0)
        self.assertEqual(lx_laya.calibrated({"answer_confidence": 7}), 1.0)
        self.assertEqual(lx_laya.calibrated({"answer_confidence": True, "confidence": 0.2}), 0.2)

    def test_verdict(self):
        self.assertEqual(lx_laya.verdict(answer("B", 0.9), 0.65), {"keep": False, "confidence": 0.9})
        self.assertEqual(lx_laya.verdict(answer("B", 0.6), 0.65), {"keep": True, "confidence": 0.6})
        self.assertEqual(lx_laya.verdict(answer("A", 0.99), 0.65), {"keep": True, "confidence": 0.99})
        weird = answer("C", 0.99)
        self.assertTrue(lx_laya.verdict(weird, 0.3)["keep"])
        for bad in (None, {}, {"answers": {}}, {"answers": {"keep": "B"}}, [1]):
            self.assertEqual(lx_laya.verdict(bad, 0.5), {"keep": True, "confidence": 0.0})


class TestJudge(unittest.TestCase):
    def test_batches_of_16(self):
        m = FakeModel()
        d = loaded(m)
        items = [{"text": "noise %d" % i if i % 2 else "ERROR %d" % i} for i in range(40)]
        r = judge(d, family="log", task="", items=items, deadline_ms=5000)
        self.assertEqual(m.batches[1:], [16, 16, 8])
        self.assertEqual(len(r["verdicts"]), 40)
        self.assertEqual(r["verdicts"][0], {"keep": True, "confidence": 0.8})
        self.assertEqual(r["verdicts"][1], {"keep": False, "confidence": 0.9})
        self.assertIsInstance(r["model_ms"], int)

    def test_family_thresholds(self):
        d = loaded()
        items = [{"text": "noise"}]
        self.assertFalse(judge(d, family="log", items=items)["verdicts"][0]["keep"])
        self.assertFalse(judge(d, family="listing", items=items)["verdicts"][0]["keep"])
        self.assertTrue(judge(d, family="listing", items=items, min_conf=0.95)["verdicts"][0]["keep"])
        self.assertFalse(judge(d, family="lines", items=items, min_conf=0.01)["verdicts"][0]["keep"])

    def test_task_reaches_the_model(self):
        m = FakeModel()
        d = loaded(m)
        judge(d, family="diff", task="rename parseMode", items=[{"text": "a"}])
        self.assertIn("working on: rename parseMode.", m.questions[-1]["keep"]["instructions"])

    def test_deadline(self):
        clock = Clock()
        m = FakeModel(clock=clock, ms_per_item=100)
        d = loaded(m, clock)
        d.ms_per_item = 100
        r = judge(d, family="log", items=[{"text": "noise"}] * 40, deadline_ms=2450)
        judged = sum(m.batches[1:])
        self.assertEqual(m.batches[1:], [16, 8])
        self.assertEqual(judged, 24)
        self.assertTrue(all(not v["keep"] for v in r["verdicts"][:judged]))
        self.assertEqual(r["verdicts"][judged:], [{"keep": True, "confidence": 0.0}] * 16)
        self.assertEqual(d.last["judged"], 24)

    def test_deadline_zero_and_busy_model(self):
        d = loaded()
        r = judge(d, family="log", items=[{"text": "noise"}] * 3, deadline_ms=0)
        self.assertEqual(r["verdicts"], [{"keep": True, "confidence": 0.0}] * 3)
        d.lock.acquire()
        try:
            t = time.monotonic()
            r = judge(d, family="log", items=[{"text": "noise"}] * 2, deadline_ms=50)
            self.assertLess(time.monotonic() - t, 1.0)
        finally:
            d.lock.release()
        self.assertEqual(r["verdicts"], [{"keep": True, "confidence": 0.0}] * 2)

    def test_short_results_are_kept(self):
        class Short(FakeModel):
            def predict(self, texts, qs):
                return [answer("B", 0.99)]
        d = loaded(Short())
        r = judge(d, family="log", items=[{"text": "a"}, {"text": "b"}])
        self.assertEqual(r["verdicts"], [{"keep": False, "confidence": 0.99}, {"keep": True, "confidence": 0.0}])

    def test_empty(self):
        self.assertEqual(judge(loaded(), family="log", items=[]), {"verdicts": [], "model_ms": 0})

    def test_not_loaded(self):
        d = lx_laya.Daemon(FakeModel())
        self.assertEqual(judge(d, family="log", items=[{"text": "x"}]), {"error": "loading the model"})
        self.assertFalse(d.handle_line(b'{"op":"ping"}')["loaded"])

    def test_load_failure(self):
        d = lx_laya.Daemon(FakeModel(fail="load"))
        self.assertFalse(d.load())
        self.assertTrue(d.stopping.is_set())
        self.assertIn("no weights", judge(d, family="log", items=[{"text": "x"}])["error"])
        self.assertIn("no weights", d.ping()["error"])

    def test_model_error(self):
        m = FakeModel()
        d = loaded(m)
        m.fail = "predict"
        r = judge(d, family="log", items=[{"text": "x"}])
        self.assertIn("RuntimeError: boom", r["error"])
        self.assertFalse(d.lock.locked())

    def test_errors_are_logged_without_their_text(self):
        m = FakeModel()
        d = loaded(m)

        def boom(texts, qs):
            raise ValueError("cannot score " + texts[0])
        m.predict = boom
        lx_laya.log.disabled = False
        self.addCleanup(setattr, lx_laya.log, "disabled", True)
        with self.assertLogs(lx_laya.log, "ERROR") as logs:
            r = judge(d, family="log", task="rotate the key", items=[{"text": "API_KEY=hunter2"}])
        self.assertIn("ValueError", r["error"])
        self.assertIn("judge failed: ValueError", logs.output[0])
        self.assertNotIn("hunter2", "\n".join(logs.output))

    def test_bad_input_never_raises(self):
        d = loaded()
        for line in (b"", b"{", b"[]", b"null", b'"x"', b"\xff\xfe", b"[" * 100000,
                     b'{"op":"nope"}', b'{"op":null}', b'{"op":"judge"}',
                     b'{"op":"judge","family":"log"}',
                     b'{"op":"judge","family":"logs","items":[]}',
                     b'{"op":"judge","family":"log","items":{}}',
                     b'{"op":"judge","family":"log","items":[1]}',
                     b'{"op":"judge","family":"log","items":[{"text":5}]}',
                     b'{"op":"judge","family":"log","items":[],"task":3}',
                     b'{"op":"judge","family":"log","items":[],"deadline_ms":"soon"}',
                     b'{"op":"judge","family":"log","items":[],"deadline_ms":true}',
                     b'{"op":"judge","family":"log","items":[],"min_conf":NaN}'):
            r = d.handle_line(line)
            self.assertIn("error", r, line[:60])
            json.dumps(r, allow_nan=False)
        for family in ('["log"]', '{"log":1}'):
            r = d.handle_line(('{"op":"judge","family":%s,"items":[]}' % family).encode())
            self.assertTrue(r["error"].startswith("family must be one of"), r)
        too_many = json.dumps({"op": "judge", "family": "log", "items": [{"text": ""}] * (lx_laya.MAX_ITEMS + 1)})
        self.assertIn("at most", d.handle_line(too_many.encode())["error"])
        r = d.handle_line(b'{"op":"judge","family":"log","items":[{"text":"x"}],"deadline_ms":1e308,"task":null}')
        self.assertEqual(len(r["verdicts"]), 1)

    def test_ping_and_status(self):
        d = loaded()
        p = d.handle_line(b'{"op":"ping"}')
        self.assertEqual(p, {"ok": True, "model": "fake/model", "loaded": True, "version": 1, "pid": os.getpid()})
        judge(d, family="log", items=[{"text": "x"}] * 3)
        s = d.handle_line(b'{"op":"status"}')
        self.assertEqual((s["requests"], s["items"], s["last"]["items"], s["last"]["judged"]), (1, 3, 3, 3))
        self.assertGreaterEqual(s["last"]["ago_s"], 0)
        self.assertNotIn("at", s["last"])
        self.assertGreater(s["rss_bytes"], 0)
        json.dumps(s, allow_nan=False)


def short_tmp():
    return tempfile.mkdtemp(prefix="lxl")


def call(path, *reqs, timeout=5):
    with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as s:
        s.settimeout(timeout)
        s.connect(path)
        with s.makefile("rb") as f:
            out = []
            for r in reqs:
                s.sendall((r if isinstance(r, bytes) else json.dumps(r).encode()) + b"\n")
                out.append(json.loads(f.readline()))
            return out


class TestServer(unittest.TestCase):
    def setUp(self):
        self.dir = short_tmp()
        self.sock = os.path.join(self.dir, "run", "laya.sock")
        self.pid = os.path.join(self.dir, "run", "laya.pid")
        old = os.umask(0o022)
        self.addCleanup(os.umask, old)
        self.addCleanup(shutil.rmtree, self.dir, True)

    def start(self, model=None):
        d = lx_laya.Daemon(model or FakeModel())
        srv = lx_laya.Server(d, self.sock, self.pid)
        srv.bind()
        d.load()
        t = threading.Thread(target=srv.serve, daemon=True)
        t.start()

        def stop():
            d.stopping.set()
            t.join(5)
            srv.close()
        self.addCleanup(stop)
        return d, srv, t

    def test_modes_and_files(self):
        self.start()
        self.assertEqual(stat.S_IMODE(os.stat(os.path.dirname(self.sock)).st_mode), 0o700)
        self.assertEqual(stat.S_IMODE(os.stat(self.sock).st_mode), 0o600)
        self.assertEqual(stat.S_IMODE(os.stat(self.pid).st_mode), 0o600)
        with open(self.pid) as f:
            self.assertEqual(f.read().strip(), str(os.getpid()))

    def test_requests_over_the_socket(self):
        self.start()
        ping, verdicts, bad = call(self.sock, {"op": "ping"},
                                   {"op": "judge", "family": "log", "task": "", "items": [{"text": "noise"}, {"text": "E"}], "deadline_ms": 2000},
                                   b"not json")
        self.assertTrue(ping["loaded"])
        self.assertEqual([v["keep"] for v in verdicts["verdicts"]], [False, True])
        self.assertIn("bad json", bad["error"])
        self.assertTrue(call(self.sock, {"op": "ping"})[0]["ok"])

    def test_oversized_request(self):
        self.start()
        old = lx_laya.MAX_LINE
        lx_laya.MAX_LINE = 1000
        self.addCleanup(setattr, lx_laya, "MAX_LINE", old)
        self.assertIn("request over", call(self.sock, b"x" * 5000)[0]["error"])
        self.assertTrue(call(self.sock, {"op": "ping"})[0]["ok"])

    def test_shutdown_cleans_up(self):
        d, srv, t = self.start()
        self.assertEqual(call(self.sock, {"op": "shutdown"}), [{"ok": True}])
        t.join(5)
        self.assertFalse(t.is_alive())
        srv.close()
        self.assertFalse(os.path.exists(self.sock))
        self.assertFalse(os.path.exists(self.pid))

    def test_stale_socket_is_replaced(self):
        os.makedirs(os.path.dirname(self.sock))
        stale = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        stale.bind(self.sock)
        stale.close()
        self.assertTrue(os.path.exists(self.sock))
        self.start()
        self.assertTrue(call(self.sock, {"op": "ping"})[0]["ok"])

    def test_live_socket_is_not_taken(self):
        self.start()
        other = lx_laya.Server(lx_laya.Daemon(FakeModel()), self.sock)
        with self.assertRaises(lx_laya.AlreadyRunning):
            other.bind()
        self.assertTrue(call(self.sock, {"op": "ping"})[0]["ok"])

    def test_one_daemon_at_a_time(self):
        d, srv, t = self.start()
        os.unlink(self.sock)
        other = lx_laya.Server(lx_laya.Daemon(FakeModel()), self.sock)
        with self.assertRaises(lx_laya.AlreadyRunning):
            other.bind()
        self.assertFalse(os.path.lexists(self.sock))
        d.stopping.set()
        t.join(5)
        srv.close()
        other.bind()
        self.addCleanup(other.close)
        self.assertTrue(stat.S_ISSOCK(os.lstat(self.sock).st_mode))

    def test_directory_others_can_write(self):
        run = os.path.dirname(self.sock)
        os.makedirs(run)
        os.chmod(run, 0o777)
        with self.assertRaisesRegex(OSError, "others can write"):
            lx_laya.Server(lx_laya.Daemon(FakeModel()), self.sock).bind()
        self.assertFalse(os.path.lexists(self.sock))

    def test_regular_file_is_not_deleted(self):
        os.makedirs(os.path.dirname(self.sock))
        with open(self.sock, "w") as f:
            f.write("mine")
        with self.assertRaises(OSError):
            lx_laya.Server(lx_laya.Daemon(FakeModel()), self.sock).bind()
        with open(self.sock) as f:
            self.assertEqual(f.read(), "mine")

    def test_close_leaves_a_newer_daemons_files(self):
        d, srv, t = self.start()
        d.stopping.set()
        t.join(5)
        os.unlink(self.sock)
        with open(self.pid, "w") as f:
            f.write("999999\n")
        newer = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        newer.bind(self.sock)
        self.addCleanup(newer.close)
        srv.close()
        self.assertTrue(os.path.exists(self.sock))
        self.assertTrue(os.path.exists(self.pid))


FAKE_MAIN = """
import sys
sys.path.insert(0, %r)
import lx_laya, test_lx_laya
sys.exit(lx_laya.main(sys.argv[1:], model=test_lx_laya.FakeModel()))
"""


class TestProcess(unittest.TestCase):
    def test_sigterm_and_log(self):
        tmp = short_tmp()
        self.addCleanup(shutil.rmtree, tmp, True)
        sock, pid, logf = (os.path.join(tmp, n) for n in ("laya.sock", "laya.pid", "laya.log"))
        p = subprocess.Popen([sys.executable, "-B", "-c", FAKE_MAIN % HERE, "--socket", sock, "--pid", pid, "--log", logf],
                             stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        self.addCleanup(p.kill)
        ping = wait_loaded(sock, p)
        self.assertEqual(ping["pid"], p.pid)
        p.terminate()
        self.assertEqual(p.wait(10), 0)
        self.assertFalse(os.path.exists(sock) or os.path.exists(pid))
        self.assertEqual(stat.S_IMODE(os.stat(logf).st_mode), 0o600)
        with open(logf) as f:
            text = f.read()
        self.assertIn("listening on", text)
        self.assertIn("stopped", text)


def wait_loaded(sock, proc, timeout=10):
    end = time.monotonic() + timeout
    while time.monotonic() < end:
        if proc.poll() is not None:
            raise AssertionError("daemon exited with %s" % proc.returncode)
        try:
            ping = call(sock, {"op": "ping"}, timeout=1)[0]
            if ping.get("loaded"):
                return ping
        except OSError:
            pass
        time.sleep(0.05)
    raise AssertionError("daemon not ready after %ss" % timeout)


@unittest.skipUnless(os.environ.get("LX_LAYA_REAL") == "1", "LX_LAYA_REAL=1 runs the real model")
class TestRealModel(unittest.TestCase):
    def test_smoke(self):
        py = os.environ.get("LX_LAYA_PYTHON")
        if not py:
            self.fail("LX_LAYA_REAL=1 needs LX_LAYA_PYTHON: a python with laya installed and the model downloaded")
        tmp = short_tmp()
        self.addCleanup(shutil.rmtree, tmp, True)
        sock, logf = os.path.join(tmp, "laya.sock"), os.path.join(tmp, "laya.log")
        p = subprocess.Popen([py, "-B", os.path.join(HERE, "lx_laya.py"), "--socket", sock,
                              "--pid", os.path.join(tmp, "laya.pid"), "--log", logf],
                             stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        self.addCleanup(p.kill)
        try:
            wait_loaded(sock, p, timeout=180)
        except AssertionError:
            with open(logf) as f:
                print(f.read()[-3000:], file=sys.stderr)
            raise
        noise = "\n".join("081109 2036%02d 148 INFO dfs.DataNode$PacketResponder: PacketResponder 1 for block "
                          "blk_%d terminating" % (i, 3886504906413966 + i) for i in range(12))
        error = "panic: runtime error: index out of range [3] with length 3\n\ngoroutine 1 [running]:\nmain.main()\n\t/app/main.go:12 +0x1d\nexit status 2"
        t = time.monotonic()
        r = call(sock, {"op": "judge", "family": "log", "task": "fix the crash in main.go",
                        "items": [{"text": noise}, {"text": error}, {"text": noise}], "deadline_ms": 30000}, timeout=40)[0]
        took = time.monotonic() - t
        self.assertNotIn("error", r)
        self.assertEqual(len(r["verdicts"]), 3)
        self.assertTrue(r["verdicts"][1]["keep"])
        for v in r["verdicts"]:
            self.assertGreater(v["confidence"], 0)
        print("\nreal laya: %s in %.2fs (model %d ms)" % (r["verdicts"], took, r["model_ms"]), file=sys.stderr)
        self.assertEqual(call(sock, {"op": "shutdown"})[0], {"ok": True})
        self.assertEqual(p.wait(30), 0)
        self.assertFalse(os.path.exists(sock))


if __name__ == "__main__":
    unittest.main()
