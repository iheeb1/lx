package agentctx

import (
	"strings"
	"testing"

	"github.com/iheeb1/lx/internal/engine"
)

func TestInferMode(t *testing.T) {
	cases := []struct {
		text string
		want engine.Mode
	}{
		{"Now let me run the tests again to verify the fix.", engine.ModeVerify},
		{"Let me verify the build passes.", engine.ModeVerify},
		{"I'll make sure nothing else broke.", engine.ModeVerify},
		{"Let me confirm the migration applied cleanly.", engine.ModeVerify},
		{"Running the suite one more time.", engine.ModeVerify},
		{"Quick sanity check that the server starts.", engine.ModeVerify},
		{"Let me double-check the output.", engine.ModeVerify},
		{"Let me check that all tests pass now.", engine.ModeVerify},
		{"Let me see if it compiles now.", engine.ModeVerify},
		{"Let me rerun the tests.", engine.ModeVerify},
		{"Re-running the failing spec to be sure.", engine.ModeVerify},
		{"The tests should pass now; running them.", engine.ModeVerify},
		{"Let me check whether the build succeeds after the change.", engine.ModeVerify},
		{"Verifying the fix with go test.", engine.ModeVerify},
		{"Fixed the import. Run it again.", engine.ModeVerify},

		{"Let me debug this.", engine.ModeError},
		{"Why does TestFoo fail on CI? Let me look.", engine.ModeError},
		{"I'll investigate the timeout.", engine.ModeError},
		{"Let me find the root cause of the panic.", engine.ModeError},
		{"Let me trace where the nil comes from.", engine.ModeError},
		{"Let me reproduce the crash locally.", engine.ModeError},
		{"I need to figure out what is going wrong.", engine.ModeError},
		{"What's going on with the flaky test?", engine.ModeError},
		{"Let me diagnose the memory leak.", engine.ModeError},
		{"Not sure why it fails, let me look at the logs.", engine.ModeError},
		{"Debugging the handshake now.", engine.ModeError},

		{"Let me check the git log.", engine.ModeAuto},
		{"Now I'll add the new file.", engine.ModeAuto},
		{"No need to verify, it compiled.", engine.ModeAuto},
		{"I won't debug this further; committing now.", engine.ModeAuto},
		{"Let me verify the fix and investigate why the other test fails.", engine.ModeAuto},
		{"Verified: all green.", engine.ModeAuto},
		{"Building with the debug flag.", engine.ModeAuto},
		{"Let me run the tests.", engine.ModeAuto},
		{"Without confirming anything, I'll push.", engine.ModeAuto},
		{"Let me read the config.", engine.ModeAuto},
		{"", engine.ModeAuto},
		{"The tests pass. Let me commit.", engine.ModeAuto},
		{"Rather than debugging, let me revert.", engine.ModeAuto},
		{"Let's make sure the tests pass and then debug the flaky one.", engine.ModeAuto},
		{"Let me verify the output. " + strings.Repeat("Then I will list the directory contents and open the file. ", 10), engine.ModeAuto},
		{"The debug build is 40 MB; let me list the artifacts.", engine.ModeAuto},
		{"Do not rerun the tests, just format.", engine.ModeAuto},
	}
	for _, c := range cases {
		if got := inferMode(c.text); got != c.want {
			t.Errorf("inferMode(%q) = %v, want %v", c.text, got, c.want)
		}
	}
}

func TestInferModeNeedsARecentMessage(t *testing.T) {
	s := &Snapshot{Assistant: []string{"Let me verify the fix."}, textAge: []int{1}}
	if s.InferMode() != engine.ModeVerify {
		t.Fatal("a message one turn back still states the intent")
	}
	s.textAge[0] = 2
	if s.InferMode() != engine.ModeAuto {
		t.Fatal("an older message says nothing about this command")
	}
	s = &Snapshot{Assistant: []string{"Let me look.", "Let me verify the fix."}, textAge: []int{0, 0}}
	if s.InferMode() != engine.ModeAuto {
		t.Fatal("only the newest message counts")
	}
}
