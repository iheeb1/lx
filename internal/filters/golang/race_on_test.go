//go:build race

package golang

// slowdown scales time bounds: the race detector makes this code ~15x slower.
const slowdown = 20
