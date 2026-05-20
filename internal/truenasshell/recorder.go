package truenasshell

// Recorder receives optional TTY telemetry (implemented by *ui.SessionRecorder).
type Recorder interface {
	RecordData(direction string, payload []byte)
	RecordResize(cols, rows int)
	RecordError(err error)
}
