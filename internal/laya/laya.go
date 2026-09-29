// Package laya is the client of lx's optional Laya daemon (integrations/laya).
package laya

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"time"
)

const Protocol = 1

const (
	pingTimeout = 50 * time.Millisecond
	maxResponse = 16 << 20
)

var ErrUnsupported = errors.New("laya needs Unix sockets")

type Item struct {
	Text string `json:"text"`
}

type Request struct {
	Family  string  `json:"family"`
	Task    string  `json:"task"`
	Items   []Item  `json:"items"`
	MinConf float64 `json:"min_conf,omitempty"`
}

type Verdict struct {
	Keep       bool    `json:"keep"`
	Confidence float64 `json:"confidence"`
}

type Info struct {
	OK        bool    `json:"ok"`
	Model     string  `json:"model"`
	Loaded    bool    `json:"loaded"`
	Version   int     `json:"version"`
	Pid       int     `json:"pid"`
	Error     string  `json:"error,omitempty"`
	UptimeS   float64 `json:"uptime_s,omitempty"`
	LoadMS    int64   `json:"load_ms,omitempty"`
	RSS       int64   `json:"rss_bytes,omitempty"`
	RSSPeak   bool    `json:"rss_peak,omitempty"`
	Requests  int     `json:"requests,omitempty"`
	Items     int     `json:"items,omitempty"`
	MSPerItem float64 `json:"ms_per_item,omitempty"`
	Python    string  `json:"python,omitempty"`
	Laya      string  `json:"laya,omitempty"`
	Last      *Last   `json:"last,omitempty"`
}

type Last struct {
	Items   int     `json:"items"`
	Judged  int     `json:"judged"`
	ModelMS int64   `json:"model_ms"`
	TotalMS int64   `json:"total_ms"`
	AgoS    float64 `json:"ago_s"`
}

type Paths struct {
	Socket string
	Pid    string
	Log    string
	Dir    string
	Venv   string
	Script string
}

func PathsFor(getenv func(string) string, home, dataDir string) Paths {
	cache := join(cacheDir(getenv, home), "lx")
	dir := join(dataDir, "laya")
	return Paths{
		Socket: join(cache, "laya.sock"),
		Pid:    join(cache, "laya.pid"),
		Log:    join(cache, "laya.log"),
		Dir:    dir,
		Venv:   join(dir, "venv"),
		Script: join(dir, "lx_laya.py"),
	}
}

func (p Paths) VenvPython() string { return join(p.Venv, filepath.Join("bin", "python")) }

func (p Paths) Python(getenv func(string) string) (py string, fromEnv bool) {
	if v := getenv("LX_LAYA_PYTHON"); v != "" {
		return v, true
	}
	if vp := p.VenvPython(); vp != "" {
		if fi, err := os.Stat(vp); err == nil && !fi.IsDir() {
			return vp, false
		}
	}
	return "", false
}

func join(base, name string) string {
	if base == "" {
		return ""
	}
	return filepath.Join(base, name)
}

func cacheDir(getenv func(string) string, home string) string {
	if runtime.GOOS != "darwin" && runtime.GOOS != "ios" {
		if d := getenv("XDG_CACHE_HOME"); filepath.IsAbs(d) {
			return d
		}
		return join(home, ".cache")
	}
	return join(home, filepath.Join("Library", "Caches"))
}

func SocketPath() string {
	home, _ := os.UserHomeDir()
	return PathsFor(os.Getenv, home, "").Socket
}

var available = sync.OnceValue(func() bool { return ready(SocketPath(), pingTimeout) })

func Available() bool { return available() }

func ready(socket string, timeout time.Duration) bool {
	in, err := Client{Socket: socket}.Ping(timeout)
	return err == nil && in.Loaded && in.Version == Protocol
}

func Refused(err error) bool { return errors.Is(err, syscall.ECONNREFUSED) }

func Judge(req Request, timeout time.Duration) ([]Verdict, error) {
	return Client{Socket: SocketPath()}.Judge(req, timeout)
}

type Client struct {
	Socket string
}

func (c Client) Ping(timeout time.Duration) (Info, error) { return c.info("ping", timeout) }

func (c Client) Status(timeout time.Duration) (Info, error) { return c.info("status", timeout) }

func (c Client) info(op string, timeout time.Duration) (Info, error) {
	var in Info
	if err := c.call(map[string]string{"op": op}, &in, timeout); err != nil {
		return Info{}, err
	}
	if !in.OK {
		return in, fmt.Errorf("laya: %s failed: %s", op, orUnknown(in.Error))
	}
	return in, nil
}

func (c Client) Shutdown(timeout time.Duration) error {
	var r struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := c.call(map[string]string{"op": "shutdown"}, &r, timeout); err != nil {
		return err
	}
	if !r.OK {
		return fmt.Errorf("laya: shutdown refused: %s", orUnknown(r.Error))
	}
	return nil
}

func (c Client) Judge(req Request, timeout time.Duration) ([]Verdict, error) {
	if len(req.Items) == 0 {
		return []Verdict{}, nil
	}
	budget := timeout - max(timeout/10, 20*time.Millisecond)
	if budget < time.Millisecond {
		return nil, fmt.Errorf("laya: a %v timeout leaves the model no time", timeout)
	}
	wire := struct {
		Op string `json:"op"`
		Request
		DeadlineMS int64 `json:"deadline_ms"`
	}{"judge", req, budget.Milliseconds()}
	var resp struct {
		Verdicts []struct {
			Keep       *bool    `json:"keep"`
			Confidence *float64 `json:"confidence"`
		} `json:"verdicts"`
		Error string `json:"error"`
	}
	if err := c.call(wire, &resp, timeout); err != nil {
		return nil, err
	}
	if resp.Error != "" {
		return nil, errors.New("laya: " + resp.Error)
	}
	if len(resp.Verdicts) != len(req.Items) {
		return nil, fmt.Errorf("laya: %d verdicts for %d items", len(resp.Verdicts), len(req.Items))
	}
	out := make([]Verdict, len(resp.Verdicts))
	for i, v := range resp.Verdicts {
		if v.Keep == nil || v.Confidence == nil || !(*v.Confidence >= 0 && *v.Confidence <= 1) {
			return nil, fmt.Errorf("laya: malformed verdict %d", i)
		}
		out[i] = Verdict{Keep: *v.Keep, Confidence: *v.Confidence}
	}
	return out, nil
}

func (c Client) call(req, resp any, timeout time.Duration) error {
	b, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("laya: %w", err)
	}
	f, err := dial(c.Socket, time.Now().Add(timeout))
	if err != nil {
		return fmt.Errorf("laya: %w", err)
	}
	defer f.Close()
	if _, err := f.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("laya: %w", err)
	}
	line, err := readLine(f)
	if err != nil {
		return fmt.Errorf("laya: %w", err)
	}
	if err := json.Unmarshal(line, resp); err != nil {
		return fmt.Errorf("laya: bad response: %w", err)
	}
	return nil
}

func readLine(r io.Reader) ([]byte, error) {
	br := bufio.NewReaderSize(r, 4096)
	var line []byte
	for {
		chunk, err := br.ReadSlice('\n')
		line = append(line, chunk...)
		switch {
		case len(line) > maxResponse:
			return nil, errors.New("response over 16 MiB")
		case err == nil:
			return line, nil
		case err == io.EOF:
			return nil, io.ErrUnexpectedEOF
		case err != bufio.ErrBufferFull:
			return nil, err
		}
	}
}

func orUnknown(s string) string {
	if s == "" {
		return "no reason given"
	}
	return s
}
