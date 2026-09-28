// Package fs handles ls, find, du and tree.
package fs

import "github.com/iheeb1/lx/internal/engine"

func init() {
	engine.Register(ls{})
	engine.Register(find{})
	engine.Register(du{})
	engine.Register(treeCmd{})
}
