// Package mobile provides the gomobile bindings for honey.
package mobile

// LogCallback is implemented by Kotlin to receive real-time updates.
type LogCallback interface {
	OnLog(msg string)
	OnProgress(progressJSON string)
}

// VPNCallback is implemented by Kotlin to receive VPN lifecycle and traffic
// updates. State is one of: "resolving", "connecting", "connected",
// "stopping", "disconnected", "error". statsJSON carries cumulative and live
// throughput: {"up_total","down_total","up_rate","down_rate","uptime_s"}.
type VPNCallback interface {
	OnState(state string)
	OnStats(statsJSON string)
}
