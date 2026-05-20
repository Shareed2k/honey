package truenasshell

import (
	"bytes"
)

// CtrlBracketDetach is ASCII GS (Ctrl+]); honey closes the session without forwarding it.
const CtrlBracketDetach = byte(0x1d)

// StdinChunkToShell forwards p to the TrueNAS shell. Ctrl+] detaches without sending the byte.
func StdinChunkToShell(sess *Session, p []byte, rec Recorder) (detached bool, err error) {
	if len(p) == 0 {
		return false, nil
	}
	i := bytes.IndexByte(p, CtrlBracketDetach)
	if i < 0 {
		if rec != nil {
			rec.RecordData("stdin", append([]byte(nil), p...))
		}
		return false, sess.WriteBinary(p)
	}
	prefix := p[:i]
	if len(prefix) > 0 {
		if rec != nil {
			rec.RecordData("stdin", append([]byte(nil), prefix...))
		}
		if err := sess.WriteBinary(prefix); err != nil {
			return false, err
		}
	}
	return true, nil
}
