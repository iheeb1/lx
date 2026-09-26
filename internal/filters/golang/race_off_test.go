//go:build !race

package golang

// slowdown scales time bounds (see race_on_test.go).
const slowdown = 1
