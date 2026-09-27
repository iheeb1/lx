package discover

import (
	"time"

	"github.com/iheeb1/lx/internal/hook"
)

func inspect(cmd string) [][]string { return hook.Inspect(cmd).Commands }

func timeAfter(seconds int) <-chan time.Time { return time.After(time.Duration(seconds) * time.Second) }

func timeNow() time.Time                  { return time.Now() }
func timeSince(t time.Time) time.Duration { return time.Since(t) }
