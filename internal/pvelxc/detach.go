package pvelxc

import (
	"bytes"
)

// CtrlBracketDetach is the byte sent by Ctrl+] (ASCII GS). Honey treats it as "close this Proxmox
// console session" and does not forward it to the guest. Use it when the CT uses autologin on
// the console TTY so `exit` immediately opens a new session.
const CtrlBracketDetach = byte(0x1d)

// StdinChunkToPVE forwards p to the PVE console. If p contains CtrlBracketDetach, bytes before
// the first occurrence are sent, then (detached, nil) is returned and the trailing part is dropped.
func StdinChunkToPVE(pve *Session, p []byte, rec Recorder) (detached bool, err error) {
	if len(p) == 0 {
		return false, nil
	}
	i := bytes.IndexByte(p, CtrlBracketDetach)
	if i < 0 {
		if rec != nil {
			rec.RecordData("stdin", append([]byte(nil), p...))
		}
		return false, pve.WriteRawTTYInput(p)
	}
	prefix := p[:i]
	if len(prefix) > 0 {
		if rec != nil {
			rec.RecordData("stdin", append([]byte(nil), prefix...))
		}
		if err := pve.WriteRawTTYInput(prefix); err != nil {
			return false, err
		}
	}
	return true, nil
}
