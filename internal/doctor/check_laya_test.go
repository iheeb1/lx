//go:build unix

package doctor

import (
	"bufio"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/iheeb1/lx/internal/laya"
)

func layaEnv(t *testing.T) (Env, laya.Paths) {
	t.Helper()
	root, err := os.MkdirTemp("", "lxd")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	e := Env{Home: filepath.Join(root, "h"), Cwd: root, HistoryPath: filepath.Join(root, "d", "history.jsonl"), Clock: time.Now}
	return e, laya.PathsFor(func(string) string { return "" }, e.Home, filepath.Join(root, "d"))
}

func layaCheck(e Env, env map[string]string) Check {
	e.Getenv = func(k string) string { return env[k] }
	s := newState(&e)
	s.checkLaya()
	for _, c := range s.checks {
		if c.ID == "laya" {
			return c
		}
	}
	return Check{}
}

func fakeLaya(t *testing.T, socket, reply string) net.Listener {
	t.Helper()
	os.MkdirAll(filepath.Dir(socket), 0o700)
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				if _, err := bufio.NewReader(c).ReadBytes('\n'); err == nil {
					c.Write([]byte(reply + "\n"))
				}
			}()
		}
	}()
	return ln
}

func TestLayaCheck(t *testing.T) {
	e, p := layaEnv(t)
	none := map[string]string{}

	c := layaCheck(e, none)
	if c.Status != Skip || c.Message != "not set up (optional: lx laya setup)" || c.Fix != "" {
		t.Errorf("not set up: %+v", c)
	}

	c = layaCheck(e, map[string]string{"LX_LAYA_PYTHON": "/nonexistent/python"})
	if c.Status != Warn || !strings.Contains(c.Message, "LX_LAYA_PYTHON=/nonexistent/python does not exist") {
		t.Errorf("bad LX_LAYA_PYTHON: %+v", c)
	}

	os.MkdirAll(filepath.Dir(p.VenvPython()), 0o700)
	os.WriteFile(p.VenvPython(), []byte("#!/bin/sh\n"), 0o700)
	c = layaCheck(e, none)
	if c.Status != Warn || !strings.Contains(c.Message, "but not running") || c.Fix != "lx laya start" {
		t.Errorf("not running: %+v", c)
	}

	for _, tc := range []struct {
		reply, status, msg, fix string
	}{
		{`{"ok":true,"model":"fake/model","loaded":true,"version":1,"pid":42}`, OK, "daemon running (pid 42, fake/model), ping ", ""},
		{`{"ok":true,"model":"fake/model","loaded":false,"version":1,"pid":42}`, Warn, "still loading its model", ""},
		{`{"ok":true,"model":"fake/model","loaded":true,"version":9,"pid":42}`, Warn, "speaks protocol 9, this lx 1", "lx laya stop && lx laya start"},
		{`garbage`, Warn, "doesn't answer", "lx laya stop && lx laya start"},
	} {
		ln := fakeLaya(t, p.Socket, tc.reply)
		c = layaCheck(e, none)
		ln.Close()
		if c.Status != tc.status || !strings.Contains(c.Message, tc.msg) || c.Fix != tc.fix {
			t.Errorf("reply %s: %+v", tc.reply, c)
		}
	}

	live := fakeLaya(t, p.Socket, `{"ok":true,"model":"fake/model","loaded":true,"version":1,"pid":42}`)
	os.Chmod(filepath.Dir(p.Socket), 0o777)
	c = layaCheck(e, none)
	os.Chmod(filepath.Dir(p.Socket), 0o700)
	live.Close()
	if c.Status != Warn || !strings.Contains(c.Message, "others can write to") {
		t.Errorf("a socket others could have replaced: %+v", c)
	}

	ln := fakeLaya(t, p.Socket, "")
	ln.(*net.UnixListener).SetUnlinkOnClose(false)
	ln.Close()
	c = layaCheck(e, none)
	if c.Status != Warn || !strings.Contains(c.Message, "doesn't answer") {
		t.Errorf("stale socket: %+v", c)
	}
	os.Remove(p.VenvPython())
	c = layaCheck(e, none)
	if c.Status != Warn || !strings.Contains(c.Message, "stale daemon socket") || c.Fix != "lx laya stop" {
		t.Errorf("stale socket, no setup: %+v", c)
	}
}
