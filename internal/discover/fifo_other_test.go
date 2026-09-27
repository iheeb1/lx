//go:build !unix

package discover

import "errors"

func mkfifo(string) error { return errors.New("no fifos") }
