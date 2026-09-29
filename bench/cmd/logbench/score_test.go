//go:build unix

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestTemplateMatch(t *testing.T) {
	cases := []struct {
		tmpl, line string
		want       bool
	}{
		{"PacketResponder <*> for block blk_<*> terminating",
			"081109 203615 148 INFO dfs.DataNode$PacketResponder: PacketResponder 1 for block blk_38865049064139660 terminating", true},
		{"Receiving block blk_<*> src: /<*>:<*> dest: /<*>:<*>",
			"081109 204815 653 INFO dfs.DataNode$DataXceiver: Receiving block blk_579 src: /10.251.30.6:33145 dest: /10.251.30.6:50010", true},
		{"<*>:Got exception while serving blk_<*> to /<*>:",
			"081109 214043 2561 WARN dfs.DataNode$DataXceiver: 10.251.30.85:50010:Got exception while serving blk_-29 to /10.251.90.64:", true},
		// a shorter template doesn't claim a longer message that starts the same way
		{"Verification succeeded for blk_<*> on disk", "INFO x: Verification succeeded for blk_1 on disk after 3 retries", false},
		{"Verification succeeded for blk_<*>", "INFO x: Verification succeeded for blk_", false},
		{"ambient=<*>", "2000 node-1 unix.hw state_change.unavailable 1084680778 1 ambient=34", true},
		{"normal", "node-2 action start 1077804742 1 normal", true},
		{"normal", "node-2 action start 1077804742 1 abnormal state", false},
		{"session opened for user <*> by (uid=<*>)", "Jun 14 15:16:01 combo sshd(pam_unix)[19939]: session  opened for user test by (uid=0)", true},
	}
	for _, c := range cases {
		w, _ := viewLine(c.line)
		if got := compile(c.tmpl).match(w); got != c.want {
			t.Errorf("%q on %q: %v, want %v", c.tmpl, c.line, got, c.want)
		}
	}
}

func TestCutLines(t *testing.T) {
	p := compile("<*>:Got exception while serving blk_<*> to /<*>:")
	rtk := "   [×3] 081110 032126 6555 WARN dfs.DataNode$DataXceiver: 10.251.26.8:50010:Got exception while serving b..."
	w, c := viewLine(rtk)
	if c == "" || !p.shows(w, c) {
		t.Fatalf("a line cut after the message's start should count: whole %q cut %q", w, c)
	}
	w, c = viewLine("081110 032126 6555 WARN dfs.DataNode$DataXceiver: 10.251.26.8:50010:Got ex...")
	if p.shows(w, c) {
		t.Fatalf("7 visible characters of the message are not evidence: %q", c)
	}
	q := compile("Failed to renew lease for [DFSClient_<*>] for <*> seconds. Will retry shortly ...")
	w, c = viewLine("2015-10-18 18:05:59 WARN LeaseRenewer: Failed to renew lease for [DFSClient_NM_1] for 30 seconds.  Will retry shortly ...")
	if !q.shows(w, c) {
		t.Fatalf("a log line that really ends in ... is still whole: %q", w)
	}
}

func TestViewLine(t *testing.T) {
	for in, want := range map[string][2]string{
		"081109 203615 148 INFO a: b 1 c [×311]":  {"081109 203615 148 INFO a: b 1 c", ""},
		"   [×1,204] INFO a: b":                   {"INFO a: b", ""},
		"[×12] INFO scheduler <*> tick (routine)": {"INFO scheduler <*> tick", ""},
		"INFO a: something lo…":                   {"INFO a: something lo…", "INFO a: something lo"},
		"head of it …[+812 chars]… tail of it":    {"head of it …[+812 chars]… tail of it", "head of it"},
		"INFO  a:\tb": {"INFO a: b", ""},
	} {
		w, c := viewLine(in)
		if got := [2]string{w, c}; got != want {
			t.Errorf("viewLine(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestScorer(t *testing.T) {
	raw := strings.Join([]string{
		"081109 1 INFO dfs.DataNode: PacketResponder 1 for block blk_1 terminating",
		"081109 2 INFO dfs.DataNode: PacketResponder 2 for block blk_2 terminating",
		"081109 3 WARN dfs.DataXceiver: 10.0.0.1:50010:Got exception while serving blk_3 to /10.0.0.2:",
		"081109 4 INFO dfs.FSNamesystem: BLOCK* NameSystem.delete: blk_4 is added to invalidSet of 10.0.0.3:50010",
		"081109 5 INFO dfs.DataNode: <x>",
	}, "\n") + "\n"
	pr := &event{ID: "E1", Template: "PacketResponder <*> for block blk_<*> terminating"}
	ex := &event{ID: "E2", Template: "<*>:Got exception while serving blk_<*> to /<*>:"}
	de := &event{ID: "E3", Template: "BLOCK* NameSystem.delete: blk_<*> is added to invalidSet of <*>"}
	tv := &event{ID: "E4", Template: "<*>"}
	tr := &truth{events: []*event{pr, ex, de, tv}, line: []*event{pr, pr, ex, de, tv}}
	for _, e := range tr.events {
		e.pat = compile(e.Template)
	}
	isErr, from := levelErrors([]string{"INFO", "INFO", "WARN", "INFO", "INFO"}, strings.Split(strings.TrimSpace(raw), "\n"))
	if from != "level column" || !reflect.DeepEqual(isErr, []bool{false, false, true, false, false}) {
		t.Fatalf("levels: %v %s", isErr, from)
	}
	s := newScorer(raw, tr, isErr)
	if len(s.eligible) != 3 || s.eligible[tv] {
		t.Fatalf("eligible: %d (a template with no constant text is not scored)", len(s.eligible))
	}
	if got := s.score(raw); got.Templates != 3 || got.ErrKinds != 1 || got.ErrLines != 1 {
		t.Fatalf("raw scores itself %+v", got)
	}
	view := "[log: 5 lines]\n" +
		"081109 1 INFO dfs.DataNode: PacketResponder 1 for block blk_1 terminating [×2]\n" +
		"   [×1] 081109 3 WARN dfs.DataXceiver: 10.0.0.1:50010:Got exception while serving b...\n"
	got := s.score(view)
	if got.Templates != 2 || got.ErrKinds != 1 || got.ErrLines != 0 || !reflect.DeepEqual(got.Missing, []string{"E3"}) {
		t.Fatalf("view: %+v", got)
	}
}

func TestLevelsFallBackToClassifier(t *testing.T) {
	lines := []string{"Jun 14 combo sshd: authentication failure; user=root", "Jun 14 combo sshd: session opened for user test"}
	isErr, from := levelErrors([]string{"combo", "combo"}, lines)
	if from != "lx classifier" || !isErr[0] || isErr[1] {
		t.Fatalf("%v %s", isErr, from)
	}
}

func TestFixtureKinds(t *testing.T) {
	raw := "2026-09-26T05:39:29Z ERROR [orders] failed to charge order ord_1167: stripe: card_declined (reason: insufficient_funds)\n" +
		"2026-09-26T05:39:30Z INFO [http] GET /healthz 200\n"
	lines := strings.Split(strings.TrimSpace(raw), "\n")
	isErr, _ := levelErrors(nil, lines)
	s := newScorer(raw, nil, isErr)
	if len(s.errKinds) != 1 {
		t.Fatalf("kinds %q", s.errKinds)
	}
	for view, want := range map[string]int{
		"[×3] 2026-09-26T05:40:00Z ERROR [orders] failed to charge order ord_1170: stripe: card_declined (reason: insufficient_funds)": 1,
		"   2026-09-26T05:39:29Z ERROR [orders] failed to charge order ord_1167: stripe: card_declined (r...":                          1,
		"   2026-09-26T05:39:29Z ERROR [orders] fail...":                                                                               0,
	} {
		if got := s.score(view).ErrKinds; got != want {
			t.Errorf("%q: %d kinds kept, want %d", view, got, want)
		}
	}
}

func TestCountLinesWithPlaceholders(t *testing.T) {
	cases := []struct {
		tmpl, line string
		want       bool
	}{
		{"session opened for user <*> by (uid=<*>)", "[×43] combo <*> session opened for user <*> by <*> (routine)", true},
		{"session opened for user <*> by LOGIN(uid=<*>)", "[×43] combo <*> session opened for user <*> by <*> (routine)", true},
		{"<*> HIGHMEM available.", "[×12] combo <*> <*> <*> <*> (routine)", false},
		{"Mount-cache hash table entries: <*> (order: <*>, <*> bytes)", "[×4] combo kernel: <*> hash table entries: <N> <*> <*> <N> bytes) (routine)", true},
		{"check pass; user unknown", "[×4] combo kernel: <*> hash table entries: <N> <*> <*> <N> bytes) (routine)", false},
		{"Received disconnect from <*>: <*>: Bye Bye [preauth]", "[×413] LabSZ sshd[<N>]: Received disconnect from <IP>: <N>: Bye Bye [preaut…", true},
	}
	for _, c := range cases {
		w, cut := viewLine(c.line)
		g := globOf(w, cut)
		if g == "" {
			t.Fatalf("%q has placeholders", c.line)
		}
		if got := compile(c.tmpl).names(g); got != c.want {
			t.Errorf("%q named by %q: %v, want %v", c.tmpl, c.line, got, c.want)
		}
	}
	if overlap("\x00ab c\x00", "xab c") != 3 || overlap("ab", "ac") != -1 {
		t.Fatal("overlap")
	}
}

func sshTruth(t *testing.T) (string, *truth) {
	t.Helper()
	lines := []string{
		"Dec 10 11:04:18 LabSZ sshd[25499]: error: Received disconnect from 103.99.0.122: 14: No more user authentication methods available. [preauth]",
		"Dec 10 11:05:01 LabSZ sshd[25600]: Received disconnect from 10.0.0.9: 11: Bye Bye [preauth]",
		"Dec 10 07:13:56 LabSZ sshd[24227]: PAM 5 more authentication failures; logname= uid=0 euid=0 tty=ssh ruser= rhost=5.36.59.76  user=root",
		"Dec 10 07:14:02 LabSZ sshd[24300]: PAM 4 more authentication failures; logname= uid=0 euid=0 tty=ssh ruser= rhost=5.188.10.180",
		"Dec 10 08:00:00 LabSZ sshd[1]: Failed password for root from 1.2.3.4 port 22 ssh2",
		"Dec 10 08:00:00 LabSZ sshd[1]: Failed password for invalid user admin from 1.2.3.4 port 22 ssh2",
		"Dec 10 09:00:00 LabSZ sshd[2]: Did not receive identification string from 1.2.3.5",
	}
	tmpl := []string{
		"error: Received disconnect from <*>: <*>: No more user authentication methods available. [preauth]",
		"Received disconnect from <*>: <*>: Bye Bye [preauth]",
		"PAM <*> more authentication failures; logname= uid=<*> euid=<*> tty=ssh ruser= rhost=<*>  user=root",
		"PAM <*> more authentication failures; logname= uid=<*> euid=<*> tty=ssh ruser= rhost=<*>",
		"Failed password for <*> from <*> port <*> ssh2",
		"Failed password for invalid user <*> from <*> port <*> ssh2",
		// loghub labelling quirk: the template does not match its own line
		"Did not receive identification string from <*> port <*>",
	}
	tr := &truth{}
	for i, s := range tmpl {
		e := &event{ID: fmt.Sprintf("E%d", i+1), Template: s, pat: compile(s), Count: 1}
		tr.events = append(tr.events, e)
		tr.line = append(tr.line, e)
	}
	return strings.Join(lines, "\n") + "\n", tr
}

func TestCopiedLinesShowOnlyTheirTemplate(t *testing.T) {
	raw, tr := sshTruth(t)
	s := newScorer(raw, tr, make([]bool, len(tr.line)))
	if len(s.eligible) != 6 || s.eligible[tr.events[6]] {
		t.Fatalf("eligible %d: a template that misses its own line is left out", len(s.eligible))
	}
	for view, want := range map[string][]string{
		// rtk cuts at 100 characters; the visible end rules out "Bye Bye"
		"   Dec 10 11:04:18 LabSZ sshd[25499]: error: Received disconnect from 103.99.0.122: 14: No more user...": {"E1"},
		// "rhost=<*>" would swallow "  user=root"
		"Dec 10 07:13:56 LabSZ sshd[24227]: PAM 5 more authentication failures; logname= uid=0 euid=0 tty=ssh ruser= rhost=5.36.59.76  user=root [×2]": {"E3"},
		"Dec 10 08:00:00 LabSZ sshd[1]: Failed password for invalid user admin from 1.2.3.4 port 22 ssh2":                                              {"E6"},
		// cut before the two lines part ways: it shows neither
		"Dec 10 08:00:00 LabSZ sshd[1]: Failed password for ...": nil,
		// not a log line: matched against the templates
		"Failed password for root from 9.9.9.9 port 1 ssh2":                                     {"E5"},
		"[×3] LabSZ sshd[<N>]: Received disconnect from <IP>: <N>: Bye Bye [preauth] (routine)": {"E2"},
	} {
		sc := s.score(view)
		var got []string
		for _, e := range tr.events[:6] {
			if !slices.Contains(sc.Missing, e.ID) {
				got = append(got, e.ID)
			}
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%q shows %v, want %v", view, got, want)
		}
	}
}

func TestRescoreKeepsTheRun(t *testing.T) {
	raw, tr := sshTruth(t)
	dir := t.TempDir()
	c := &logCase{id: "OpenSSH", source: "loghub", raw: raw, tr: tr, isErr: make([]bool, len(tr.line)), levels: "lx classifier"}
	view := "Dec 10 11:04:18 LabSZ sshd[25499]: error: Received disconnect from 103.99.0.122: 14: No more user...\n"
	if err := os.MkdirAll(filepath.Join(dir, "logbench", "OpenSSH"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "logbench", "OpenSSH", "rtk.txt"), []byte(view), 0o644); err != nil {
		t.Fatal(err)
	}
	stale := variant{Key: "rtk", Tokens: 41, MedianMs: 19.2, File: filepath.Join("logbench", "OpenSSH", "rtk.txt"),
		Laya: &layaUse{FoldedRuns: 2}, score: score{Templates: 5}}
	b, _ := json.Marshal(report{Runs: 5, Cases: []caseOut{{ID: "OpenSSH", Templates: 99, Variants: []variant{stale}}}})
	out := filepath.Join(dir, "logbench.json")
	if err := os.WriteFile(out, b, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := rescore(out, []*logCase{c}); err != nil {
		t.Fatal(err)
	}
	var rep report
	b, _ = os.ReadFile(out)
	if err := json.Unmarshal(b, &rep); err != nil {
		t.Fatal(err)
	}
	co := rep.Cases[0]
	v := co.Variants[0]
	if co.Templates != 6 || v.Templates != 1 || !reflect.DeepEqual(v.Missing, []string{"E2", "E3", "E4", "E5", "E6"}) {
		t.Fatalf("not rescored: case %d templates, view %+v", co.Templates, v.score)
	}
	if rep.Runs != 5 || v.Tokens != 41 || v.MedianMs != 19.2 || v.Laya == nil || v.Laya.FoldedRuns != 2 {
		t.Fatalf("the run's measurements changed: %+v", v)
	}
}

func TestJudgedPerRequest(t *testing.T) {
	d := &layaDaemon{log: filepath.Join(t.TempDir(), "laya.log")}
	write := func(s string, flag int) {
		f, err := os.OpenFile(d.log, flag|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		f.WriteString(s)
		f.Close()
	}
	write("2026-09-28 10:00:00,001 INFO judge log: 6/24 items in 1080 ms (model 1050 ms)\n", os.O_TRUNC)
	if got := d.judged(); !reflect.DeepEqual(got, []string{"6/24"}) {
		t.Fatalf("%q", got)
	}
	write("x INFO judge log: 24/24 items in 990 ms (model 980 ms)\nx INFO judge lines: 3/3 items in 90 ms (model 88 ms)\n", os.O_APPEND)
	if got := d.judged(); !reflect.DeepEqual(got, []string{"24/24", "3/3"}) {
		t.Fatalf("only the new requests: %q", got)
	}
	write("x INFO judge log: 1/2 items in 9 ms (model 8 ms)\n", os.O_TRUNC)
	if got := d.judged(); !reflect.DeepEqual(got, []string{"1/2"}) {
		t.Fatalf("after the log rotated: %q", got)
	}
}
