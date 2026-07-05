// webui/src/RecipesTab/index.tsx
import { useCallback, useState, Suspense, lazy } from 'react';
import { Steps, message, Modal, Button } from 'antd';
import type { HostRecord } from '../HostPicker';
import { parseDiskRecipe, fetchRecipeContent } from '../api/recipes';
import { type ParsedRecipe, type RecentRunEntry } from '../api/types/recipes';
import { reconcileHosts } from '../hostReconcile';
import { SessionReplayModal } from '../SessionReplayModal';
import { StepHosts } from './StepHosts';
import { StepRecipe } from './StepRecipe';
import { StepPlan } from './StepPlan';
import { StepRun } from './StepRun';
import { saveDraft } from './drafts';
import type { Draft, WizardStep } from './types';
import { WizardProvider, useWizard } from './WizardContext';
import { useRecipeAssist } from '../contexts/RecipeAssistContext';
import { useHostSelection } from '../contexts/HostSelectionContext';
import { useAppContext } from '../contexts/AppContext';
import './recipes-tab.css';

const HighlightedCode = lazy(async () => import('../HighlightedCode').then((m) => ({ default: m.HighlightedCode })));

function detectCodeLanguage(fileName: string): 'cue' | 'yaml' {
  const lowerName = fileName.toLowerCase();
  if (lowerName.endsWith('.yaml') || lowerName.endsWith('.yml')) {
    return 'yaml';
  }
  return 'cue';
}

function CodeLoadingFallback({ code }: { code: string }) {
  return (
    <pre
      style={{
        margin: 0,
        fontSize: '0.78rem',
        whiteSpace: 'pre-wrap',
        wordBreak: 'break-word',
        overflow: 'auto',
        padding: '0.65rem',
        border: '1px solid #2a3140',
        borderRadius: 6,
        background: '#0f1115',
      }}
    >
      {code}
    </pre>
  );
}

function RecipesTabInner() {
  const { state, dispatch } = useWizard();
  const { records, setSelectedKeys } = useHostSelection();
  const { meta } = useAppContext();
  const { openRecipeAssist } = useRecipeAssist();
  const [hostReconcileNote, setHostReconcileNote] = useState<string | null>(null);
  const [replayRecord, setReplayRecord] = useState<HostRecord | null>(null);
  const [replayFileName, setReplayFileName] = useState<string | null>(null);
  const [recipePreview, setRecipePreview] = useState<{ title: string; content: string } | null>(null);

  const openRecipePreview = async (path: string, name: string) => {
    setRecipePreview({ title: name, content: 'Loading…' });
    try {
      const content = await fetchRecipeContent(path);
      setRecipePreview({ title: name, content });
    } catch (e) {
      setRecipePreview({
        title: name,
        content: e instanceof Error ? e.message : String(e),
      });
    }
  };

  const setHosts = useCallback(
    (h: HostRecord[]) => {
      dispatch({ type: 'SET_HOSTS', payload: h });
      const next: Record<string, boolean> = {};
      for (const host of h) next[recordKey(host)] = true;
      setSelectedKeys(next);
    },
    [setSelectedKeys, dispatch],
  );

  function go(step: WizardStep) {
    dispatch({ type: 'GO_STEP', payload: step });
  }

  async function pickDisk(path: string) {
    try {
      const parsed = await fetchRecipeContentParsed(path);
      dispatch({ type: 'SET_BASE_RECIPE', payload: parsed });
      setHostReconcileNote(null);
      dispatch({ type: 'SET_RECIPE_REF', payload: { kind: 'disk', path }, edits: parsed });
    } catch (e) {
      void message.error(`Could not load recipe: ${e instanceof Error ? e.message : String(e)}`);
    }
  }

  function pickDraft(d: Draft) {
    dispatch({ type: 'SET_BASE_RECIPE', payload: d.recipe });
    setHostReconcileNote(null);
    dispatch({ type: 'SET_RECIPE_REF', payload: { kind: 'draft', id: d.id }, edits: d.recipe });
  }

  async function pickRecent(r: RecentRunEntry) {
    try {
      const parsed = await fetchRecipeContentParsed(r.recipe_path);
      dispatch({ type: 'SET_BASE_RECIPE', payload: parsed });
      setHostReconcileNote(null);
      dispatch({ type: 'SET_RECIPE_REF', payload: { kind: 'disk', path: r.recipe_path }, edits: parsed });
    } catch (e) {
      void message.error(`Could not load recipe: ${e instanceof Error ? e.message : String(e)}`);
    }
  }

  async function rerunSameHosts(r: RecentRunEntry) {
    if (!r.hosts?.length) {
      return;
    }
    const { matched, missing, total } = reconcileHosts(r.hosts, records);
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
    if (!state.edits || !state.baseRecipe) return;
    const d = saveDraft({
      name,
      baseRecipePath: state.recipe && state.recipe.kind === 'disk' ? state.recipe.path : '',
      recipe: state.edits,
    });
    dispatch({ type: 'SET_RECIPE_REF', payload: { kind: 'draft', id: d.id }, edits: state.edits });
  }

  return (
    <div className="recipes-tab">
      <Steps
        type="navigation"
        size="small"
        current={state.step - 1}
        onChange={(current) => {
          const nextStep = (current + 1) as WizardStep;
          if (nextStep <= state.step) {
            go(nextStep);
          }
        }}
        items={[
          {
            title: 'Hosts',
            status: state.step > 1 ? 'finish' : state.step === 1 ? 'process' : 'wait',
          },
          {
            title: 'Recipe',
            disabled: state.step < 2,
            status: state.step > 2 ? 'finish' : state.step === 2 ? 'process' : 'wait',
          },
          {
            title: 'Plan',
            disabled: state.step < 3,
            status: state.step > 3 ? 'finish' : state.step === 3 ? 'process' : 'wait',
          },
          {
            title: 'Run',
            disabled: state.step < 4,
            status: state.step === 4 ? 'process' : 'wait',
          },
        ]}
      />

      {state.step === 1 ? (
        <StepHosts
          records={records}
          onHostsChange={setHosts}
          onNext={() => go(2)}
          reconcileNote={hostReconcileNote}
        />
      ) : null}

      {state.step === 2 ? (
        <StepRecipe
          sessionRecordingAvailable={!!meta?.session_recording_available}
          onBack={() => go(1)}
          onPickDisk={pickDisk}
          onPickDraft={pickDraft}
          onPickRecent={pickRecent}
          onReplay={openReplay}
          onRerunSameHosts={rerunSameHosts}
          onViewSource={openRecipePreview}
          onAiAssist={openRecipeAssist}
        />
      ) : null}

      {state.step === 3 && state.edits && state.baseRecipe ? (
        <StepPlan
          onSaveAsDraft={onSaveAsDraft}
          sessionRecordingAvailable={!!meta?.session_recording_available}
          onBack={() => go(2)}
          onExecute={() => go(4)}
        />
      ) : null}

      {state.step === 4 && state.edits ? (
        <StepRun
          sessionRecordingAvailable={!!meta?.session_recording_available}
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
            dispatch({ type: 'RESET', payload: { hosts: state.hosts } });
            setHostReconcileNote(null);
          }}
        />
      ) : null}

      {replayRecord && replayFileName ? (
        <SessionReplayModal
          record={replayRecord}
          recordings={[{ file_name: replayFileName, modified_unix_ms: 0, size_bytes: 0 }]}
          assistAvailable={!!meta?.terminal_assist_available}
          onClose={() => {
            setReplayRecord(null);
            setReplayFileName(null);
          }}
        />
      ) : null}

      <Modal maskClosable={false} open={!!recipePreview}
        title={recipePreview?.title}
        onCancel={() => setRecipePreview(null)}
        footer={<Button onClick={() => setRecipePreview(null)}>Close</Button>}
        width="min(720px, 96vw)"
        styles={{ body: { maxHeight: '80vh', overflow: 'auto', padding: 0 } }}
      >
        {recipePreview && (
          <Suspense fallback={<CodeLoadingFallback code={recipePreview.content} />}>
            <HighlightedCode
              className="recipe-preview-code"
              code={recipePreview.content}
              language={detectCodeLanguage(recipePreview.title)}
            />
          </Suspense>
        )}
      </Modal>
    </div>
  );
}

export function RecipesTab() {
  const { selectedRecords } = useHostSelection();
  return (
    <WizardProvider initialHosts={selectedRecords}>
      <RecipesTabInner />
    </WizardProvider>
  );
}

async function fetchRecipeContentParsed(path: string): Promise<ParsedRecipe> {
  return parseDiskRecipe(path);
}
