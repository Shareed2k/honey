//go:build !windows

package main

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// raiseFDLimit lifts the process open-file (RLIMIT_NOFILE) limit toward a sane
// target. honey holds one socket per cached SSH connection for the duration of a
// batch, so a parallel exec across hundreds of hosts accumulates hundreds of
// open FDs. On macOS the default soft limit is 256 — once the cache fills, new
// connections can't be opened and the run stalls silently at ~236/N with nothing
// in the log. Best-effort: any error leaves the inherited limit in place.
func raiseFDLimit() {
	var lim unix.Rlimit
	if unix.Getrlimit(unix.RLIMIT_NOFILE, &lim) != nil {
		return
	}
	const target = 65536
	if lim.Cur >= target {
		return
	}
	// Set BOTH cur and max to a concrete value. Two macOS constraints force this:
	// Setrlimit rejects RLIM_INFINITY for NOFILE, and the soft limit can't exceed
	// the hard limit — so the hard limit must be raised too (a process may raise
	// its own hard limit up to kern.maxfilesperproc). Step down for kernels whose
	// per-process file cap is below the target.
	for _, want := range []uint64{target, 24576, 10240, 4096} {
		if want <= lim.Cur {
			break
		}
		if unix.Setrlimit(unix.RLIMIT_NOFILE, &unix.Rlimit{Cur: want, Max: want}) == nil {
			fmt.Fprintf(os.Stderr, "honey: raised open-file limit %d -> %d\n", lim.Cur, want)
			return
		}
	}
	fmt.Fprintf(os.Stderr,
		"honey: could not raise open-file limit (currently %d); a large parallel exec may stall — run `ulimit -n 65536` before honey\n",
		lim.Cur)
}
