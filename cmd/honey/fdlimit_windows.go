//go:build windows

package main

// raiseFDLimit is a no-op on Windows, which has no RLIMIT_NOFILE.
func raiseFDLimit() {}
