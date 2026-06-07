package ui

import "github.com/shareed2k/honey/internal/cuetry"

// remoteOpts returns the SSH/fan-out options for a step, or nil for local-only
// steps (template, ai) that do not implement cuetry.RemoteStep. The cuetry helpers
// that consume *RemoteExec are nil-safe.
func remoteOpts(s cuetry.Step) *cuetry.RemoteExec {
	if rs, ok := s.(cuetry.RemoteStep); ok {
		return rs.Remote()
	}
	return nil
}

// tunnelOf returns the tunnel block for a tunnel step, or nil.
func tunnelOf(s cuetry.Step) *cuetry.RecipeStepTunnel {
	if ts, ok := s.(*cuetry.TunnelStep); ok {
		return ts.Tunnel
	}
	return nil
}
