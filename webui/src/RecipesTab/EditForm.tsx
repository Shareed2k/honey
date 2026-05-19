// webui/src/RecipesTab/EditForm.tsx
import { useEffect, useMemo, useState } from 'react';
import {
  validateRecipeContent,
  type ParsedRecipe,
  type ParsedRecipeEnvFrom,
  type ParsedRecipeStep,
  type ParsedRecipeStepTemplate,
  type ValidationError,
} from '../api';

type Props = {
  recipe: ParsedRecipe;
  baseRecipe: ParsedRecipe;
  onChange: (next: ParsedRecipe) => void;
  onErrors: (errors: ValidationError[] | null) => void;
  onSaveAsDraft: (name: string) => void;
};

const EDITABLE_KINDS = new Set(['command', 'script', 'ai', 'template']);

function stepKind(s: ParsedRecipeStep): string {
  if (s.command !== undefined) return 'command';
  if (s.script) return 'script';
  if (s.ai) return 'ai';
  if (s.template) return 'template';
  if (s.agent_transfer) return 'agent_transfer';
  if (s.notify) return 'notify';
  return 'unknown';
}

function templateDataJson(t: ParsedRecipeStepTemplate | undefined): string {
  if (!t?.data || Object.keys(t.data).length === 0) return '';
  try {
    return JSON.stringify(t.data, null, 2);
  } catch {
    return '';
  }
}

function parseDependsText(text: string): string[] {
  return text
    .split(/[\s,]+/)
    .map((s) => s.trim())
    .filter(Boolean);
}

function dependsText(depends: string[] | undefined): string {
  return depends?.join(', ') ?? '';
}

export function EditForm({ recipe, baseRecipe, onChange, onErrors, onSaveAsDraft }: Props) {
  const dirty = useMemo(() => JSON.stringify(recipe) !== JSON.stringify(baseRecipe), [recipe, baseRecipe]);
  const [draftName, setDraftName] = useState('');
  const isGraph = recipe.type === 'graph';
  const [dataJsonByStep, setDataJsonByStep] = useState<Record<number, string>>({});

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

  function updateTemplate(i: number, patch: Partial<ParsedRecipeStepTemplate>) {
    const s = recipe.steps[i];
    updateStep(i, { template: { ...(s.template ?? { template: '' }), ...patch } });
  }

  function commitTemplateData(i: number, raw: string) {
    setDataJsonByStep((prev) => ({ ...prev, [i]: raw }));
    const trimmed = raw.trim();
    if (trimmed === '') {
      updateTemplate(i, { data: undefined });
      return;
    }
    try {
      const parsed = JSON.parse(trimmed) as Record<string, unknown>;
      if (parsed === null || typeof parsed !== 'object' || Array.isArray(parsed)) {
        return;
      }
      updateTemplate(i, { data: parsed });
    } catch {
      // keep last valid data until JSON parses
    }
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

  function updateEnvFrom(i: number, refs: ParsedRecipeEnvFrom[]) {
    updateStep(i, { env_from: refs.length > 0 ? refs : undefined });
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
        const dataJson = dataJsonByStep[i] ?? templateDataJson(s.template);
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
                {isGraph ? (
                  <>
                    <label className="rcp-edit__field">
                      id
                      <input
                        value={s.id ?? ''}
                        onChange={(e) => updateStep(i, { id: e.target.value || undefined })}
                      />
                    </label>
                    <label className="rcp-edit__field">
                      depends (comma-separated)
                      <input
                        value={dependsText(s.depends)}
                        onChange={(e) =>
                          updateStep(i, {
                            depends: parseDependsText(e.target.value).length
                              ? parseDependsText(e.target.value)
                              : undefined,
                          })
                        }
                      />
                    </label>
                  </>
                ) : null}
                <label className="rcp-edit__field">
                  host
                  {kind === 'template' ? (
                    <input
                      value={s.host ?? '_'}
                      onChange={(e) => updateStep(i, { host: e.target.value || '_' })}
                      title='Use "_" for a single local render; "*" or a host name for per-host templates (capture output only with "_")'
                    />
                  ) : (
                    <input value={s.host ?? ''} onChange={(e) => updateStep(i, { host: e.target.value })} />
                  )}
                </label>
                {kind === 'template' ? (
                  <p className="rcp-edit__hint">
                    Local Go <code>text/template</code> step. Template body is not Honey-expanded; use{' '}
                    <code>data</code> with <code>${'${VAR}'}</code> for capture variables.
                  </p>
                ) : null}
                {kind !== 'template' ? (
                  <label className="rcp-edit__field">
                    run_as
                    <input
                      value={s.run_as ?? ''}
                      onChange={(e) => updateStep(i, { run_as: e.target.value || undefined })}
                    />
                  </label>
                ) : null}
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
                {kind === 'template' ? (
                  <>
                    <label className="rcp-edit__field rcp-edit__field--multiline">
                      template (Go template body)
                      <textarea
                        value={s.template?.template ?? ''}
                        rows={Math.min(16, Math.max(3, (s.template?.template ?? '').split('\n').length))}
                        onChange={(e) => updateTemplate(i, { template: e.target.value })}
                      />
                    </label>
                    <label className="rcp-edit__field rcp-edit__field--multiline">
                      data (JSON object)
                      <textarea
                        value={dataJson}
                        rows={6}
                        spellCheck={false}
                        onChange={(e) => commitTemplateData(i, e.target.value)}
                        onBlur={(e) => commitTemplateData(i, e.target.value)}
                      />
                    </label>
                    <label className="rcp-edit__field">
                      output (capture variable name)
                      <input
                        value={s.template?.output ?? ''}
                        placeholder="e.g. RESULT"
                        onChange={(e) => updateTemplate(i, { output: e.target.value || undefined })}
                      />
                    </label>
                    {isGraph ? (
                      <TemplateEnvFromEditor
                        refs={s.env_from ?? []}
                        onChange={(refs) => updateEnvFrom(i, refs)}
                      />
                    ) : null}
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

function TemplateEnvFromEditor({
  refs,
  onChange,
}: {
  refs: ParsedRecipeEnvFrom[];
  onChange: (refs: ParsedRecipeEnvFrom[]) => void;
}) {
  function updateRef(idx: number, patch: Partial<ParsedRecipeEnvFrom>) {
    const next = refs.map((r, j) => (j === idx ? { ...r, ...patch } : r));
    onChange(next);
  }
  function addRef() {
    onChange([...refs, { step: '', map: { VAR: 'stdout' } }]);
  }
  function removeRef(idx: number) {
    onChange(refs.filter((_, j) => j !== idx));
  }
  return (
    <fieldset className="rcp-edit__env-from">
      <legend>env_from</legend>
      {refs.map((ref, idx) => (
        <div key={idx} className="rcp-edit__env-from-row">
          <label className="rcp-edit__field">
            step id
            <input
              value={ref.step ?? ''}
              onChange={(e) =>
                updateRef(idx, {
                  step: e.target.value || undefined,
                  from_output: e.target.value ? undefined : ref.from_output,
                })
              }
            />
          </label>
          <label className="rcp-edit__field">
            from_output
            <input
              value={ref.from_output ?? ''}
              onChange={(e) =>
                updateRef(idx, {
                  from_output: e.target.value || undefined,
                  step: e.target.value ? undefined : ref.step,
                })
              }
            />
          </label>
          <label className="rcp-edit__field">
            env key → stdout
            <input
              value={Object.keys(ref.map)[0] ?? ''}
              onChange={(e) => {
                const k = e.target.value || 'VAR';
                updateRef(idx, { map: { [k]: 'stdout' } });
              }}
            />
          </label>
          <button type="button" className="rcp-btn rcp-btn--ghost rcp-btn--small" onClick={() => removeRef(idx)}>
            remove
          </button>
        </div>
      ))}
      <button type="button" className="rcp-btn rcp-btn--ghost rcp-btn--small" onClick={addRef}>
        add env_from
      </button>
    </fieldset>
  );
}
