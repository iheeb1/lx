//go:build !unix

package laya

import (
	"os"
	"syscall"
	"time"
)

func dial(string, time.Time) (*os.File, error) { return nil, ErrUnsupported }

func DetachAttr() *syscall.SysProcAttr { return nil }
