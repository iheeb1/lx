//go:build !(darwin || dragonfly || freebsd || linux || netbsd || openbsd)

package track

func lockTune(path string) (func(), error) { return lockTuneExcl(path) }
