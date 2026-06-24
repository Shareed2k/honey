// webui/src/RecipesTab/StepRun.tsx
import { useEffect, useRef, useState } from 'react';
import { Button, Typography } from 'antd';
import { cueExecStream } from '../api/exec';
import { type CueExecRequest, type HostExecResultRow } from '../api/types/exec';
import type { LiveState } from './types';
import { useWizard } from './WizardContext';

type Props = {
  sessionRecordingAvailable: boolean;
  onViewRecording: (fileName: string) => void;
  onRunAgain: () => void;
  onStartNew: () => void;
  onRow?: (row: HostExecResultRow) => void;
  onStatusChange?: (status: LiveState['status']) => void;
};

export function StepRun(props: Props) {
  const { state } = useWizard();
  const { edits: recipe, recipe: recipeRef, hosts, envOverrides, sshUser, recordSession } = state;
  const recipeBasePath = recipeRef?.kind === 'disk' ? recipeRef.path : null;

  const [localState, setLocalState] = useState<LiveState>({ rows: [], status: 'idle' });
  const [recordingFileName, setRecordingFileName] = useState<string | null>(null);
  const abortRef = useRef<AbortController | null>(null);

  useEffect(() => {
    if (!recipe) return;
    const ctrl = new AbortController();
    abortRef.current = ctrl;
    setLocalState({ rows: [], status: 'running' });
    props.onStatusChange?.('running');
    setRecordingFileName(null);

    const env = envOverrides
      .filter((p) => p.key.trim())
      .map((p) => `${p.key}=${p.value}`);

    const payload: CueExecRequest = {
      recipe_path: recipeBasePath ?? undefined,
      recipe_content: recipeBasePath ? undefined : recipe,
      execute: true,
      ssh_user: sshUser,
      records: hosts,
      env,
      record_session: recordSession,
    };

    cueExecStream(payload, (row: HostExecResultRow) => {
      if (ctrl.signal.aborted) return;
      props.onRow?.(row);
      setLocalState((s) => ({ ...s, rows: [...s.rows, row] }));
    }, ctrl.signal)
      .then((footer) => {
        if (ctrl.signal.aborted) return;
        if (footer.recording_id) {
          setRecordingFileName(`${footer.recording_id}.hrec.jsonl`);
        }
        setLocalState((s) => ({ ...s, status: 'ok' }));
        props.onStatusChange?.('ok');
      })
      .catch((e: unknown) => {
        if (e instanceof Error && e.name === 'AbortError') return;
        console.error('cueExecStream failed:', e);
        setLocalState((s) => ({ ...s, status: 'err' }));
        props.onStatusChange?.('err');
      });

    return () => {
      ctrl.abort();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  function handleCancel() {
    abortRef.current?.abort();
    setLocalState((s) => ({ ...s, status: 'idle' }));
    props.onStatusChange?.('idle');
  }

  const ok = localState.rows.filter((r) => r.Success).length;
  const err = localState.rows.filter((r) => !r.Success).length;
  const pending = hosts.length - localState.rows.length;
  const canViewRecording =
    recordSession && props.sessionRecordingAvailable && !!recordingFileName && localState.status !== 'running';

  return (
    <div className="rcp-step rcp-step--run">
      <header className="rcp-step__header">
        <Typography.Title level={5} style={{ margin: 0 }}>④ Run</Typography.Title>
        <div className="rcp-run-summary">
          <span className="rcp-ok">{ok} ok</span> ·{' '}
          <span className="rcp-err">{err} err</span> ·{' '}
          <span className="rcp-pend">{Math.max(0, pending)} pending</span>
          <span> · status: {localState.status}</span>
        </div>
        {localState.status === 'running' ? (
          <Button onClick={handleCancel}>
            cancel run
          </Button>
        ) : null}
      </header>

      <ul className="rcp-run__hosts">
        {localState.rows.map((row, i) => (
          <li
            key={`${row.Name}-${i}`}
            className={'rcp-run__host ' + (row.Success ? 'ok' : 'err')}
          >
            <header>
              <span className="rcp-run__hostname">{row.Name}</span>
              <span className="rcp-run__hostip">{row.IP}</span>
              <span className="rcp-run__status">
                {row.Success ? '✓' : `✗ exit ${row.ExitCode}`}
              </span>
            </header>
            {row.Output ? <pre className="rcp-run__out">{row.Output}</pre> : null}
            {row.ErrMsg ? <pre className="rcp-run__err">{row.ErrMsg}</pre> : null}
          </li>
        ))}
      </ul>

      {localState.status !== 'running' ? (
        <footer className="rcp-step__footer">
          {canViewRecording ? (
            <Button
              onClick={() => props.onViewRecording(recordingFileName!)}
            >
              View recording
            </Button>
          ) : null}
          <Button onClick={props.onStartNew}>
            start new
          </Button>
          <Button type="default" onClick={props.onRunAgain}>
            Run again
          </Button>
        </footer>
      ) : null}
    </div>
  );
}
