//go:build !unix

package tee

func fallbackDir() string { return "" }

func ownedPrivate(string) bool { return false }
