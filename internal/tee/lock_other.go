//go:build !(darwin || dragonfly || freebsd || linux || netbsd || openbsd)

package tee

import "os"

// No flock here: liveness falls back to the pid (pidAlive).
const haveLocks = false

func lockPart(*os.File) {}

func partHeld(string) int { return unknown }
