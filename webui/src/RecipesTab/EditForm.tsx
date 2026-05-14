// webui/src/RecipesTab/EditForm.tsx
import { useEffect, useMemo, useState } from 'react';
import { validateRecipeContent, type ParsedRecipe, type ParsedRecipeStep, type ValidationError } from '../api';

type Props = {
  recipe: ParsedRecipe;
  baseRecipe: ParsedRecipe;
  onChange: (next: ParsedRecipe) => void;
  onErrors: (errors: ValidationError[] | null) => void;
  onSaveAsDraft: (name: string) => void;
};

const EDITABLE_KINDS = new Set(['command', 'script', 'ai']);

function stepKind(s: ParsedRecipeStep): string {
  if (s.command !== undefined) return 'command';
  if (s.script) return 'script';
  if (s.ai) return 'ai';
  if (s.agent_transfer) return 'agent_transfer';
  if (s.notify) return 'notify';
  return 'unknown';
}

export function EditForm({ recipe, baseRecipe, onChange, onErrors, onSaveAsDraft }: Props) {
  const dirty = useMemo(() => JSON.stringify(recipe) !== JSON.stringify(baseRecipe), [recipe, baseRecipe]);
  const [draftName, setDraftName] = useState('');

  useEffect(() => {
    const t = setTimeout(async () => {
      const res = await validateRecipeContent(recipe);
      onErrors('errors' in res ? res.errors : null);
    }, 300);
    return () => clearTimeout(t);
  }, [recipe, onErrors]);

  function updateStep(i: number, patch: Partial<ParsedRecipeStep>) {
    const steps = recipe.steps.map((s, j) => (j === i ? { ...s, ...patch } : s));
    onChange({ ...recipe, steps });
  }
  function moveStep(i: number, dir: -1 | 1) {
    const j = i + dir;
    if (j < 0 || j >= recipe.steps.length) return;
    const steps = recipe.steps.slice();
    [steps[i], steps[j]] = [steps[j], steps[i]];
    onChange({ ...recipe, steps });
  }
  function removeStep(i: number) {
    onChange({ ...recipe, steps: recipe.steps.filter((_, j) => j !== i) });
  }
  function duplicateStep(i: number) {
    const cloned = JSON.parse(JSON.stringify(recipe.steps[i])) as ParsedRecipeStep;
    const steps = [...recipe.steps];
    steps.splice(i + 1, 0, cloned);
    onChange({ ...recipe, steps });
  }

  return (
    <div className="rcp-edit">
      {dirty ? (
        <div className="rcp-edit__dirty">
          <span>Modified</span>
          <input
            placeholder={`${baseRecipe.name} — copy`}
            value={draftName}
            onChange={(e) => setDraftName(e.target.value)}
          />
          <button type="button" onClick={() => onSaveAsDraft(draftName || `${baseRecipe.name} — copy`)}>
            save as draft
          </button>
        </div>
      ) : null}

      {recipe.steps.map((s, i) => {
        const kind = stepKind(s);
        const editable = EDITABLE_KINDS.has(kind);
        return (
          <div key={i} className="rcp-edit__step">
            <header>
              <span className="rcp-edit__idx">{i + 1}</span>
              <span className="rcp-edit__kind">{kind}</span>
              <span className="rcp-edit__actions">
                <button type="button" onClick={() => moveStep(i, -1)} aria-label="move up">↑</button>
                <button type="button" onClick={() => moveStep(i, +1)} aria-label="move down">↓</button>
                <button type="button" onClick={() => duplicateStep(i)} aria-label="duplicate">⎘</button>
                <button type="button" onClick={() => removeStep(i)} aria-label="delete">✕</button>
              </span>
            </header>

            {!editable ? (
              <p className="rcp-edit__readonly">
                Step kind <code>{kind}</code> is not yet editable in the web form. It will run as-is.
              </p>
            ) : (
              <>
                <label className="rcp-edit__field">
                  host
                  <input
                    value={s.host ?? ''}
                    onChange={(e) => updateStep(i, { host: e.target.value })}
                  />
                </label>
                <label className="rcp-edit__field">
                  run_as
                  <input
                    value={s.run_as ?? ''}
                    onChange={(e) => updateStep(i, { run_as: e.target.value || undefined })}
                  />
                </label>
                {kind === 'command' ? (
                  <label className="rcp-edit__field rcp-edit__field--multiline">
                    command
                    <textarea
                      value={s.command ?? ''}
                      rows={Math.min(12, Math.max(2, (s.command ?? '').split('\n').length))}
                      onChange={(e) => updateStep(i, { command: e.target.value })}
                    />
                  </label>
                ) : null}
                {kind === 'script' ? (
                  <label className="rcp-edit__field rcp-edit__field--multiline">
                    script body
                    <textarea
                      value={s.script?.body ?? ''}
                      rows={Math.min(20, Math.max(3, (s.script?.body ?? '').split('\n').length))}
                      onChange={(e) => updateStep(i, { script: { ...(s.script ?? {}), body: e.target.value } })}
                    />
                  </label>
                ) : null}
                {kind === 'ai' ? (
                  <>
                    <label className="rcp-edit__field">
                      model
                      <input
                        value={s.ai?.model ?? ''}
                        onChange={(e) => updateStep(i, { ai: { ...(s.ai ?? {}), model: e.target.value } })}
                      />
                    </label>
                    <label className="rcp-edit__field rcp-edit__field--multiline">
                      prompt
                      <textarea
                        value={s.ai?.prompt ?? ''}
                        rows={6}
                        onChange={(e) => updateStep(i, { ai: { ...(s.ai ?? {}), prompt: e.target.value } })}
                      />
                    </label>
                  </>
                ) : null}
              </>
            )}
          </div>
        );
      })}
    </div>
  );
}
