// webui/src/RecipesTab/StepRun.tsx
import { useEffect, useRef, useState } from 'react';
import { Button, Typography } from 'antd';
import { cueExecStream, type CueExecRequest, type HostExecResultRow, type ParsedRecipe } from '../api';
import type { HostRecord } from '../HostPicker';
import type { EnvPair, LiveState } from './types';

type Props = {
  recipe: ParsedRecipe;
  recipeBasePath: string | null; // null if running from a draft (uses recipe_content)
  hosts: HostRecord[];
  envOverrides: EnvPair[];
  sshUser: string;
  recordSession: boolean;
  sessionRecordingAvailable: boolean;
  onViewRecording: (fileName: string) => void;
  onRunAgain: () => void;
  onStartNew: () => void;
  onRow?: (row: HostExecResultRow) => void;
  onStatusChange?: (status: LiveState['status']) => void;
};

export function StepRun(props: Props) {
  const [state, setState] = useState<LiveState>({ rows: [], status: 'idle' });
  const [recordingFileName, setRecordingFileName] = useState<string | null>(null);
  const abortRef = useRef<AbortController | null>(null);

  useEffect(() => {
    const ctrl = new AbortController();
    abortRef.current = ctrl;
    setState({ rows: [], status: 'running' });
    props.onStatusChange?.('running');
    setRecordingFileName(null);

    const env = props.envOverrides
      .filter((p) => p.key.trim())
      .map((p) => `${p.key}=${p.value}`);

    const payload: CueExecRequest = {
      recipe_path: props.recipeBasePath ?? undefined,
      recipe_content: props.recipeBasePath ? undefined : props.recipe,
      execute: true,
      ssh_user: props.sshUser,
      records: props.hosts,
      env,
      record_session: props.recordSession,
    };

    cueExecStream(payload, (row: HostExecResultRow) => {
      if (ctrl.signal.aborted) return;
      props.onRow?.(row);
      setState((s) => ({ ...s, rows: [...s.rows, row] }));
    }, ctrl.signal)
      .then((footer) => {
        if (ctrl.signal.aborted) return;
        if (footer.recording_id) {
          setRecordingFileName(`${footer.recording_id}.hrec.jsonl`);
        }
        setState((s) => ({ ...s, status: 'ok' }));
        props.onStatusChange?.('ok');
      })
      .catch((e: unknown) => {
        if (e instanceof Error && e.name === 'AbortError') return;
        console.error('cueExecStream failed:', e);
        setState((s) => ({ ...s, status: 'err' }));
        props.onStatusChange?.('err');
      });

    return () => {
      ctrl.abort();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  function handleCancel() {
    abortRef.current?.abort();
    setState((s) => ({ ...s, status: 'idle' }));
    props.onStatusChange?.('idle');
  }

  const ok = state.rows.filter((r) => r.Success).length;
  const err = state.rows.filter((r) => !r.Success).length;
  const pending = props.hosts.length - state.rows.length;
  const canViewRecording =
    props.recordSession && props.sessionRecordingAvailable && !!recordingFileName && state.status !== 'running';

  return (
    <div className="rcp-step rcp-step--run">
      <header className="rcp-step__header">
        <Typography.Title level={5} style={{ margin: 0 }}>④ Run</Typography.Title>
        <div className="rcp-run-summary">
          <span className="rcp-ok">{ok} ok</span> ·{' '}
          <span className="rcp-err">{err} err</span> ·{' '}
          <span className="rcp-pend">{Math.max(0, pending)} pending</span>
          <span> · status: {state.status}</span>
        </div>
        {state.status === 'running' ? (
          <Button onClick={handleCancel}>
            cancel run
          </Button>
        ) : null}
      </header>

      <ul className="rcp-run__hosts">
        {state.rows.map((row, i) => (
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

      {state.status !== 'running' ? (
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
