import { useState } from 'react';
import { useWorkspaceStore } from './store';
import { ParameterPromptModal } from '../ParameterPromptModal';
import { recipeNameFromFilename } from '../useRecipeGraph';
import type { RecipePrompt } from '../../api/types/recipes';

export type RunMode = 'upstream' | 'downstream';

type PendingRun = { stepId: string | null; mode: RunMode };

type RecipeDefaultsWithPrompts = { prompts?: Record<string, RecipePrompt> };

// Shared run-trigger gate for every place a run can be kicked off (Editor
// Header's "Run recipe", StepEditorPanel's "Run Step"/"Resume from here") —
// ported from the old (pre-dockview) StudioWorkspace.tsx's
// prepareStepRun/doPrepareStepRun + <ParameterPromptModal> wiring (see git
// history of webui/src/RecipeStudio/StudioWorkspace.tsx), adapted to the
// multi-doc store: instead of a single `pendingRun` in component state, each
// trigger site owns its own hook instance keyed on its `recipeId`.
//
// `run(stepId, mode)`:
//   - if the recipe's `defaults.prompts` is a non-empty object, stashes the
//     (stepId, mode) as pending and opens the prompt modal instead of
//     starting the run immediately.
//   - otherwise calls store.startRun right away with extraEnv [].
// Submitting the modal converts its values ({[key]: value}) to the
// {key,value}[] shape StepRun's envOverrides expects and calls startRun with
// the pending (stepId, mode). Cancelling drops the pending run — no store
// mutation.
//
// Render `promptModal` once in the trigger component's JSX (it's a no-op,
// closed <ParameterPromptModal> when nothing is pending).
export function useRunTrigger(recipeId: string | null) {
  const startRun = useWorkspaceStore((s) => s.startRun);
  const recipeDefaults = useWorkspaceStore((s) => (recipeId ? s.docs[recipeId]?.recipeDefaults : undefined));
  const [pending, setPending] = useState<PendingRun | null>(null);

  const prompts = (recipeDefaults as RecipeDefaultsWithPrompts | undefined)?.prompts ?? {};
  const hasPrompts = Object.keys(prompts).length > 0;

  const run = (stepId: string | null, mode: RunMode = 'upstream') => {
    if (!recipeId) return;
    if (hasPrompts) {
      setPending({ stepId, mode });
      return;
    }
    startRun(recipeId, stepId, mode, []);
  };

  const promptModal = (
    <ParameterPromptModal
      open={pending !== null}
      prompts={prompts}
      recipeName={recipeNameFromFilename(recipeId ?? undefined)}
      onCancel={() => setPending(null)}
      onSubmit={(vals) => {
        const extraEnv = Object.entries(vals).map(([key, value]) => ({ key, value }));
        if (pending && recipeId) {
          startRun(recipeId, pending.stepId, pending.mode, extraEnv);
        }
        setPending(null);
      }}
    />
  );

  return { run, promptModal };
}
