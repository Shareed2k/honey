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

// computeShareWatchScale is the WATCHFIT-1 fill factor: scaling the
// (unscaled) content box up or down so it fills the container on whichever
// axis is tighter, never cropping the other axis. Pulled out as a pure
// function so the math has one direct test instead of only being exercised
// indirectly through DOM/xterm plumbing. Returns 1 (no-op) for a
// not-yet-measurable box (zero/negative dimensions) rather than Infinity or
// NaN.
export function computeShareWatchScale(containerW: number, containerH: number, contentW: number, contentH: number): number {
  if (containerW <= 0 || containerH <= 0 || contentW <= 0 || contentH <= 0) {
    return 1;
  }
  return Math.min(containerW / contentW, containerH / contentH);
}

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
    el.style.overflow = 'hidden';
    el.style.position = 'relative';

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

    // WATCHFIT-1: once the server has told us the guest's REAL window size
    // (a "size" control frame), term.resize() is authoritative — fit() must
    // never run again, since fit() sizes cols/rows to the CONTAINER, which is
    // exactly what fought the server's size and drew the guest's session into
    // a truncated corner of the modal. Instead the terminal's own root
    // element is CSS-scaled up (or down) to fill the container, so nothing is
    // cropped and the guest's geometry is never touched.
    let serverSize: { cols: number; rows: number } | null = null;

    const rescale = () => {
      if (!serverSize || !term.element) {
        return;
      }
      const root = term.element;
      root.style.transform = 'none';
      const k = computeShareWatchScale(el.clientWidth, el.clientHeight, root.offsetWidth, root.offsetHeight);
      root.style.transformOrigin = 'top left';
      root.style.transform = `scale(${k})`;
    };

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
          const j = JSON.parse(ev.data) as { error?: string; size?: { cols?: number; rows?: number } };
          if (j.error) {
            term.writeln(`\r\n\x1b[31m${j.error}\x1b[0m`);
          } else if (j.size && j.size.cols && j.size.rows) {
            serverSize = { cols: j.size.cols, rows: j.size.rows };
            term.resize(j.size.cols, j.size.rows);
            requestAnimationFrame(rescale);
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

    // Before the server's first size frame, behave exactly as before (fit to
    // container). Afterward, the terminal's cols/rows are fixed to the
    // guest's real size — a container resize (e.g. the modal's open
    // transition) only needs a rescale, never a re-fit.
    const onResize = () => (serverSize ? rescale() : fit.fit());
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
      // Viewport-relative, not a fixed 800x420 box: the view is locked to the
      // GUEST's geometry (a real session is routinely ~200x57, which is roughly
      // 1450x970 CSS px at this font size), so a small dialog forced a ~0.43
      // downscale — tiny text plus a wide empty margin from the aspect-ratio
      // mismatch. Giving the dialog the window instead means the scale lands
      // near 1 for a typical session, and only an unusually large one is
      // shrunk at all.
      width="95vw"
      style={{ top: 24, maxWidth: '100vw', paddingBottom: 0 }}
      title={resourceName ? `Watching ${resourceName} (read-only)` : 'Watching (read-only)'}
      footer={[
        <Button key="close" onClick={onClose}>
          Close
        </Button>,
      ]}
    >
      <div style={{ height: '78vh' }} ref={setEl} />
    </Modal>
  );
}
