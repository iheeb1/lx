// Package filters links the built-in filters.
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
