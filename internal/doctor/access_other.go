//go:build !unix

package doctor

// writable cannot be answered without writing on this platform.
func writable(p string) (ok, known bool) { return false, false }
