// Package laya embeds the script `lx laya` runs as its daemon.
package laya

import _ "embed"

//go:embed lx_laya.py
var Script string
