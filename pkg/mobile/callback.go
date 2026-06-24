// Package mobile provides the gomobile bindings for honey.
package mobile

// LogCallback is implemented by Kotlin to receive real-time updates.
type LogCallback interface {
	OnLog(msg string)
	OnProgress(progressJSON string)
}
