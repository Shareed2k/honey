// webui/src/RecipesTab/index.tsx
import { useCallback, useState } from 'react';
import type { HostRecord } from '../HostPicker';
import { parseDiskRecipe, type ParsedRecipe, type RecentRunEntry } from '../api';
import { reconcileHosts } from '../hostReconcile';
import { SessionReplayModal } from '../SessionReplayModal';
import { StepHosts } from './StepHosts';
import { StepRecipe } from './StepRecipe';
import { StepPlan } from './StepPlan';
import { StepRun } from './StepRun';
import { saveDraft } from './drafts';
import type { Draft, WizardStep, WizardState } from './types';
import { INITIAL_WIZARD_STATE } from './types';
import './recipes-tab.css';

type Props = {
  records: HostRecord[];
  selectedRecords: HostRecord[];
  onSelectedRecordsChange: (h: HostRecord[]) => void;
  onViewSource: (path: string, name: string) => void;
  onAiAssist: (path: string, name: string) => void;
  sessionRecordingAvailable: boolean;
  terminalAssistAvailable: boolean;
};

export function RecipesTab(props: Props) {
  const [state, setState] = useState<WizardState>({
    ...INITIAL_WIZARD_STATE,
    hosts: props.selectedRecords,
  });
  const [baseRecipe, setBaseRecipe] = useState<ParsedRecipe | null>(null);
  const [hostReconcileNote, setHostReconcileNote] = useState<string | null>(null);
  const [replayRecord, setReplayRecord] = useState<HostRecord | null>(null);
  const [replayFileName, setReplayFileName] = useState<string | null>(null);

  const setHosts = useCallback(
    (h: HostRecord[]) => {
      setState((s) => ({ ...s, hosts: h }));
      props.onSelectedRecordsChange(h);
    },
    [props],
  );

  function go(step: WizardStep) {
    setState((s) => ({ ...s, step }));
  }

  async function pickDisk(path: string) {
    try {
      const parsed = await fetchRecipeContentParsed(path);
      setBaseRecipe(parsed);
      setHostReconcileNote(null);
      setState((s) => ({ ...s, recipe: { kind: 'disk', path }, edits: parsed, step: 3 }));
    } catch (e) {
      alert(`Could not load recipe: ${e instanceof Error ? e.message : String(e)}`);
    }
  }

  function pickDraft(d: Draft) {
    setBaseRecipe(d.recipe);
    setHostReconcileNote(null);
    setState((s) => ({ ...s, recipe: { kind: 'draft', id: d.id }, edits: d.recipe, step: 3 }));
  }

  async function pickRecent(r: RecentRunEntry) {
    try {
      const parsed = await fetchRecipeContentParsed(r.recipe_path);
      setBaseRecipe(parsed);
      setHostReconcileNote(null);
      setState((s) => ({
        ...s,
        recipe: { kind: 'disk', path: r.recipe_path },
        edits: parsed,
        step: 3,
      }));
    } catch (e) {
      alert(`Could not load recipe: ${e instanceof Error ? e.message : String(e)}`);
    }
  }

  async function rerunSameHosts(r: RecentRunEntry) {
    if (!r.hosts?.length) {
      return;
    }
    const { matched, missing, total } = reconcileHosts(r.hosts, props.records);
    setHosts(matched);
    if (missing > 0) {
      setHostReconcileNote(`${matched.length} of ${total} hosts still in inventory; ${missing} missing.`);
    } else {
      setHostReconcileNote(null);
    }
    await pickRecent(r);
    go(1);
  }

  function openReplay(run: RecentRunEntry) {
    if (!run.recording_id) {
      return;
    }
    setReplayRecord({
      provider: 'mixed',
      name: run.recipe_name || 'recipe run',
      primary_ip: '',
    });
    setReplayFileName(`${run.recording_id}.hrec.jsonl`);
  }

  function onSaveAsDraft(name: string) {
    if (!state.edits || !baseRecipe) return;
    const d = saveDraft({
      name,
      baseRecipePath: state.recipe && state.recipe.kind === 'disk' ? state.recipe.path : '',
      recipe: state.edits,
    });
    setState((s) => ({ ...s, recipe: { kind: 'draft', id: d.id } }));
  }

  return (
    <div className="recipes-tab">
      <Breadcrumb step={state.step} onJump={go} />

      {state.step === 1 ? (
        <StepHosts
          records={props.records}
          hosts={state.hosts}
          onHostsChange={setHosts}
          onNext={() => go(2)}
          reconcileNote={hostReconcileNote}
        />
      ) : null}

      {state.step === 2 ? (
        <StepRecipe
          current={state.recipe}
          sessionRecordingAvailable={props.sessionRecordingAvailable}
          onBack={() => go(1)}
          onPickDisk={pickDisk}
          onPickDraft={pickDraft}
          onPickRecent={pickRecent}
          onReplay={openReplay}
          onRerunSameHosts={rerunSameHosts}
          onViewSource={props.onViewSource}
          onAiAssist={props.onAiAssist}
        />
      ) : null}

      {state.step === 3 && state.edits && baseRecipe ? (
        <StepPlan
          recipe={state.edits}
          baseRecipe={baseRecipe}
          onRecipeChange={(next) => setState((s) => ({ ...s, edits: next }))}
          onSaveAsDraft={onSaveAsDraft}
          envOverrides={state.envOverrides}
          onEnvChange={(env) => setState((s) => ({ ...s, envOverrides: env }))}
          sshUser={state.sshUser}
          onSSHUserChange={(u) => setState((s) => ({ ...s, sshUser: u }))}
          recordSession={state.recordSession}
          onRecordSessionChange={(v) => setState((s) => ({ ...s, recordSession: v }))}
          sessionRecordingAvailable={props.sessionRecordingAvailable}
          hostCount={state.hosts.length}
          onBack={() => go(2)}
          onExecute={() => go(4)}
        />
      ) : null}

      {state.step === 4 && state.edits ? (
        <StepRun
          recipe={state.edits}
          recipeBasePath={state.recipe?.kind === 'disk' ? state.recipe.path : null}
          hosts={state.hosts}
          envOverrides={state.envOverrides}
          sshUser={state.sshUser}
          recordSession={state.recordSession}
          sessionRecordingAvailable={props.sessionRecordingAvailable}
          onViewRecording={(fileName) => {
            setReplayRecord({
              provider: 'mixed',
              name: 'recipe run',
              primary_ip: '',
            });
            setReplayFileName(fileName);
          }}
          onRunAgain={() => go(3)}
          onStartNew={() => {
            setState({ ...INITIAL_WIZARD_STATE, hosts: state.hosts });
            setBaseRecipe(null);
            setHostReconcileNote(null);
          }}
        />
      ) : null}

      {replayRecord && replayFileName ? (
        <SessionReplayModal
          record={replayRecord}
          recordings={[{ file_name: replayFileName, modified_unix_ms: 0, size_bytes: 0 }]}
          assistAvailable={props.terminalAssistAvailable}
          onClose={() => {
            setReplayRecord(null);
            setReplayFileName(null);
          }}
        />
      ) : null}
    </div>
  );
}

function Breadcrumb({ step, onJump }: { step: WizardStep; onJump: (s: WizardStep) => void }) {
  const labels: Record<WizardStep, string> = { 1: 'Hosts', 2: 'Recipe', 3: 'Plan', 4: 'Run' };
  return (
    <nav className="rcp-breadcrumb" aria-label="wizard steps">
      {([1, 2, 3, 4] as WizardStep[]).map((n) => (
        <button
          key={n}
          type="button"
          className={
            'rcp-breadcrumb__dot' +
            (n === step
              ? ' rcp-breadcrumb__dot--cur'
              : n < step
                ? ' rcp-breadcrumb__dot--done'
                : '')
          }
          onClick={() => (n <= step ? onJump(n) : undefined)}
          disabled={n > step}
        >
          <span className="rcp-breadcrumb__num">{n}</span>
          <span className="rcp-breadcrumb__label">{labels[n]}</span>
        </button>
      ))}
    </nav>
  );
}

async function fetchRecipeContentParsed(path: string): Promise<ParsedRecipe> {
  return parseDiskRecipe(path);
}
