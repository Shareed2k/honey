//go:build !windows

package main

import "golang.org/x/sys/unix"

// raiseFDLimit lifts the process open-file (RLIMIT_NOFILE) soft limit toward the
// hard limit, capped at a sane target. honey holds one socket per cached SSH
// connection, so a large parallel exec (hundreds of hosts) can exhaust the
// default soft limit — 256 on macOS — and then stall silently once the cache
// fills and new connections can no longer be opened. Best-effort: any error
// leaves the inherited limit in place.
func raiseFDLimit() {
	var lim unix.Rlimit
	if unix.Getrlimit(unix.RLIMIT_NOFILE, &lim) != nil {
		return
	}
	const target = 65536
	if lim.Cur >= target {
		return
	}
	// Try target, then step down: some kernels (macOS) reject a soft limit above
	// their per-process file cap even when the hard limit looks higher.
	for _, want := range []uint64{target, 24576, 10240, 4096} {
		if want <= lim.Cur {
			break
		}
		newCur := want
		if lim.Max != 0 && want > lim.Max {
			newCur = lim.Max
		}
		if newCur <= lim.Cur {
			continue
		}
		if unix.Setrlimit(unix.RLIMIT_NOFILE, &unix.Rlimit{Cur: newCur, Max: lim.Max}) == nil {
			return
		}
	}
}
