import { useEffect, useState } from 'react';
import { Button, Modal } from 'antd';
import { Terminal } from '@xterm/xterm';
import { FitAddon } from '@xterm/addon-fit';
import { shareWatchWebSocketURL } from '../api/jit';

export type ShareWatchModalProps = {
  /** The grant whose guest session to watch, or null when the modal is closed. */
  grantId: string | null;
  resourceName?: string;
  onClose: () => void;
};

// ShareWatchModal is the OPERATOR's authed, read-only live view of a guest's
// access-request session (GET /ws/share/watch). It never wires xterm's onData
// (or a resize frame) into the socket at all — this is a pure viewer; the
// server enforces the same thing independently (tmux `-r` attach, no stdin
// wired in), but the client-side omission means there is no path here that
// could even try to influence the guest's session.
export function ShareWatchModal({ grantId, resourceName, onClose }: ShareWatchModalProps) {
  // A state-backed callback ref, NOT useRef: the Modal below is destroyOnHidden,
  // so its body (and this container) mounts into antd's portal in a later commit
  // than the one that sets grantId. An effect keyed on [grantId] alone therefore
  // ran while containerRef.current was still null, bailed out, and never re-ran
  // (a ref changing does not re-trigger an effect) — the modal opened empty with
  // no terminal and no socket. Storing the element in state re-runs the effect
  // the moment the div actually exists.
  const [el, setEl] = useState<HTMLDivElement | null>(null);

  useEffect(() => {
    if (!grantId || !el) {
      return undefined;
    }

    const term = new Terminal({
      cursorBlink: false,
      fontSize: 13,
      theme: {
        background: '#0f1115',
        foreground: '#e8e8e8',
        cursor: '#e8e8e8',
      },
    });
    const fit = new FitAddon();
    term.loadAddon(fit);
    term.open(el);
    fit.fit();

    const ws = new WebSocket(shareWatchWebSocketURL(grantId));
    ws.binaryType = 'arraybuffer';

    ws.onopen = () => {
      fit.fit();
      // The hello frame every terminal WS expects; the server ignores its
      // contents for this read-only route (see handleShareWatch).
      ws.send(JSON.stringify({ cols: term.cols, rows: term.rows }));
    };

    ws.onmessage = (ev) => {
      if (typeof ev.data === 'string') {
        try {
          const j = JSON.parse(ev.data) as { error?: string };
          if (j.error) {
            term.writeln(`\r\n\x1b[31m${j.error}\x1b[0m`);
          }
        } catch {
          /* ignore malformed control frame */
        }
        return;
      }
      term.write(new Uint8Array(ev.data as ArrayBuffer));
    };

    ws.onclose = () => {
      term.writeln('\r\n\x1b[33m[watch ended]\x1b[0m');
    };

    const onResize = () => fit.fit();
    const ro = new ResizeObserver(onResize);
    ro.observe(el);

    return () => {
      ro.disconnect();
      ws.close();
      term.dispose();
    };
  }, [grantId, el]);

  return (
    <Modal
      open={!!grantId}
      onCancel={onClose}
      destroyOnHidden
      width={800}
      title={resourceName ? `Watching ${resourceName} (read-only)` : 'Watching (read-only)'}
      footer={[
        <Button key="close" onClick={onClose}>
          Close
        </Button>,
      ]}
    >
      <div style={{ height: 420 }} ref={setEl} />
    </Modal>
  );
}
