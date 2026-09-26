// Package filters links every built-in command filter into the binary.
// Each sub-package registers its filters with the engine from init().
//
// Order matters only between packages that could claim the same command;
// Go initializes these imports in import-path order.
package filters

import (
	_ "github.com/iheeb1/lx/internal/filters/build"
	_ "github.com/iheeb1/lx/internal/filters/data"
	_ "github.com/iheeb1/lx/internal/filters/fs"
	_ "github.com/iheeb1/lx/internal/filters/git"
	_ "github.com/iheeb1/lx/internal/filters/golang"
	_ "github.com/iheeb1/lx/internal/filters/infra"
	_ "github.com/iheeb1/lx/internal/filters/jstest"
	_ "github.com/iheeb1/lx/internal/filters/jstools"
	_ "github.com/iheeb1/lx/internal/filters/python"
	_ "github.com/iheeb1/lx/internal/filters/search"
)
