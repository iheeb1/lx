//go:build !(darwin || dragonfly || freebsd || linux || netbsd || openbsd)

package tee

import "os"

const haveLocks = false

func lockPart(*os.File) {}

func partHeld(string) int { return unknown }
