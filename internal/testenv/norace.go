//go:build !race

package testenv

// Race reports whether the binary was built with -race.
const Race = false
