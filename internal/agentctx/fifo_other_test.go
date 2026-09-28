//go:build !unix

package agentctx

import "errors"

func mkfifo(string) error { return errors.New("no fifos") }
