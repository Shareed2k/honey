// webui/src/RecipesTab/StepRun.tsx
import { useEffect, useState } from 'react';
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
};

export function StepRun(props: Props) {
  const [state, setState] = useState<LiveState>({ rows: [], status: 'idle' });
  const [recordingFileName, setRecordingFileName] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    setState({ rows: [], status: 'running' });
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
      if (cancelled) {
        return;
      }
      setState((s) => ({ ...s, rows: [...s.rows, row] }));
    })
      .then((footer) => {
        if (cancelled) {
          return;
        }
        if (footer.recording_id) {
          setRecordingFileName(`${footer.recording_id}.hrec.jsonl`);
        }
        setState((s) => ({ ...s, status: 'ok' }));
      })
      .catch((e: unknown) => {
        if (cancelled) {
          return;
        }
        console.error('cueExecStream failed:', e);
        setState((s) => ({ ...s, status: 'err' }));
      });

    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  function handleCancel() {
    console.warn('cueExecStream does not support cancellation; ignoring cancel click');
  }

  const ok = state.rows.filter((r) => r.Success).length;
  const err = state.rows.filter((r) => !r.Success).length;
  const pending = props.hosts.length - state.rows.length;
  const canViewRecording =
    props.recordSession && props.sessionRecordingAvailable && !!recordingFileName && state.status !== 'running';

  return (
    <div className="rcp-step rcp-step--run">
      <header className="rcp-step__header">
        <h2>④ Run</h2>
        <div className="rcp-run-summary">
          <span className="rcp-ok">{ok} ok</span> ·{' '}
          <span className="rcp-err">{err} err</span> ·{' '}
          <span className="rcp-pend">{Math.max(0, pending)} pending</span>
          <span> · status: {state.status}</span>
        </div>
        {state.status === 'running' ? (
          <button type="button" className="rcp-btn" onClick={handleCancel}>
            cancel run
          </button>
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
            <button
              type="button"
              className="rcp-btn"
              onClick={() => props.onViewRecording(recordingFileName!)}
            >
              View recording
            </button>
          ) : null}
          <button type="button" className="rcp-btn" onClick={props.onStartNew}>
            start new
          </button>
          <button type="button" className="rcp-btn rcp-btn--pri" onClick={props.onRunAgain}>
            Run again
          </button>
        </footer>
      ) : null}
    </div>
  );
}
