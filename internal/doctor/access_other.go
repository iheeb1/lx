//go:build !unix

package doctor

func writable(p string) (ok, known bool) { return false, false }
