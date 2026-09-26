// Package fs condenses file-system listings: ls, find/fd, du and tree.
//
// All of them are Content filters: a file named error_test.go is data, not
// a failure. Every filter keeps the tool's diagnostics ("find: x:
// Permission denied") verbatim and first, keeps every listed path
// reconstructable (directory line + name) except inside pruned heavy
// directories and past explicit caps, which always carry exact counts.
package fs

import "github.com/iheeb1/lx/internal/engine"

func init() {
	engine.Register(ls{})
	engine.Register(find{})
	engine.Register(du{})
	engine.Register(treeCmd{})
}
