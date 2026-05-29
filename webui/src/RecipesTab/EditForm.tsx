// webui/src/RecipesTab/EditForm.tsx
import { useEffect, useMemo, useRef, useState } from 'react';
import { Button, Input, Select } from 'antd';
import {
  validateRecipeContent,
  type ParsedRecipe,
  type ParsedRecipeEnvFrom,
  type ParsedRecipeStep,
  type ParsedRecipeStepTemplate,
  type RecipeGraphPlan,
  type ResolvedStep,
  type ValidationError,
} from '../api';
import {
  ADD_STEP_KINDS,
  appendRecipeStep,
  canAddStepKind,
  EDITABLE_KINDS,
  stepKind,
  stepSupportsNotify,
  stepSupportsRetry,
  type RecipeStepKind,
} from './recipeStepUtils';
import {
  GraphStepFields,
  HostField,
  RunAsField,
  StepAgentTransferEditor,
  StepCommandEditor,
  StepFileTransferEditor,
  StepNotifyEditor,
  StepPluginEditor,
  StepRetryEditor,
  StepScriptEditor,
  StepTunnelEditor,
} from './StepEditors';

type Props = {
  recipe: ParsedRecipe;
  baseRecipe: ParsedRecipe;
  onChange: (next: ParsedRecipe) => void;
  onErrors: (errors: ValidationError[]) => void;
  onValidated: (res: { plan: string; steps: ResolvedStep[]; graph?: RecipeGraphPlan }) => void;
  onSaveAsDraft: (name: string) => void;
};

function templateDataJson(t: ParsedRecipeStepTemplate | undefined): string {
  if (!t?.data || Object.keys(t.data).length === 0) return '';
  try {
    return JSON.stringify(t.data, null, 2);
  } catch {
    return '';
  }
}

export function EditForm({ recipe, baseRecipe, onChange, onErrors, onValidated, onSaveAsDraft }: Props) {
  const dirty = useMemo(() => JSON.stringify(recipe) !== JSON.stringify(baseRecipe), [recipe, baseRecipe]);
  const [draftName, setDraftName] = useState('');
  const isGraph = recipe.type === 'graph';
  const [dataJsonByStep, setDataJsonByStep] = useState<Record<number, string>>({});
  const [addKind, setAddKind] = useState<RecipeStepKind>('command');
  const [showAddPicker, setShowAddPicker] = useState(false);
  const lastAddedRef = useRef<number | null>(null);

  useEffect(() => {
    const t = setTimeout(async () => {
      const res = await validateRecipeContent(recipe);
      if ('errors' in res) {
        onErrors(res.errors);
      } else {
        onValidated(res);
      }
    }, 300);
    return () => clearTimeout(t);
  }, [recipe, onErrors, onValidated]);

  useEffect(() => {
    if (lastAddedRef.current == null) return;
    const el = document.querySelector(`[data-step-index="${lastAddedRef.current}"]`);
    el?.scrollIntoView({ behavior: 'smooth', block: 'nearest' });
    lastAddedRef.current = null;
  }, [recipe.steps.length]);

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

  function addStep() {
    if (!canAddStepKind(addKind, recipe)) return;
    lastAddedRef.current = recipe.steps.length;
    onChange(appendRecipeStep(recipe, addKind));
    setShowAddPicker(false);
  }

  return (
    <div className="rcp-edit">
      {dirty ? (
        <div className="rcp-edit__dirty">
          <span>Modified</span>
          <Input
            placeholder={`${baseRecipe.name} — copy`}
            value={draftName}
            onChange={(e) => setDraftName(e.target.value)}
          />
          <Button onClick={() => onSaveAsDraft(draftName || `${baseRecipe.name} — copy`)}>
            save as draft
          </Button>
        </div>
      ) : null}

      <div className="rcp-edit__add-step">
        <Button onClick={() => setShowAddPicker((v) => !v)}>
          + Add step
        </Button>
        {showAddPicker ? (
          <span className="rcp-edit__add-step-picker">
            <Select
              value={addKind}
              onChange={(val) => setAddKind(val as RecipeStepKind)}
              options={ADD_STEP_KINDS.map((k) => ({
                value: k,
                label: k === 'ai' && !canAddStepKind('ai', recipe) ? `${k} (one per recipe)` : k,
                disabled: !canAddStepKind(k, recipe),
              }))}
            />
            <Button onClick={addStep} disabled={!canAddStepKind(addKind, recipe)}>
              append
            </Button>
          </span>
        ) : null}
      </div>

      {recipe.steps.map((s, i) => {
        const kind = stepKind(s);
        const editable = EDITABLE_KINDS.has(kind);
        const dataJson = dataJsonByStep[i] ?? templateDataJson(s.template);
        const showRunAs = kind !== 'template' && kind !== 'put' && kind !== 'get' && kind !== 'agent_transfer';
        return (
          <div key={i} className="rcp-edit__step" data-step-index={i}>
            <header>
              <span className="rcp-edit__idx">{i + 1}</span>
              <span className="rcp-edit__kind">{kind}</span>
              <span className="rcp-edit__actions">
                <Button type="text" size="small" onClick={() => moveStep(i, -1)} aria-label="move up">
                  ↑
                </Button>
                <Button type="text" size="small" onClick={() => moveStep(i, +1)} aria-label="move down">
                  ↓
                </Button>
                <Button type="text" size="small" onClick={() => duplicateStep(i)} aria-label="duplicate">
                  ⎘
                </Button>
                <Button type="text" size="small" onClick={() => removeStep(i)} aria-label="delete">
                  ✕
                </Button>
              </span>
            </header>

            {!editable ? (
              <p className="rcp-edit__readonly">
                Step kind <code>{kind}</code> is not editable in the web form.
              </p>
            ) : (
              <>
                {isGraph ? <GraphStepFields step={s} onChange={(p) => updateStep(i, p)} /> : null}
                <HostField step={s} kind={kind} onChange={(p) => updateStep(i, p)} />
                {kind === 'template' ? (
                  <p className="rcp-edit__hint">
                    Local Go <code>text/template</code> step. Use <code>data</code> with{' '}
                    <code>${'${VAR}'}</code> for capture variables.
                  </p>
                ) : null}
                {showRunAs ? <RunAsField step={s} onChange={(p) => updateStep(i, p)} /> : null}
                {kind === 'command' ? <StepCommandEditor step={s} onChange={(p) => updateStep(i, p)} /> : null}
                {kind === 'script' ? (
                  <StepScriptEditor
                    script={s.script}
                    onChange={(script) => updateStep(i, { script })}
                  />
                ) : null}
                {kind === 'put' ? (
                  <StepFileTransferEditor
                    label="put"
                    transfer={s.put}
                    onChange={(put) => updateStep(i, { put })}
                  />
                ) : null}
                {kind === 'get' ? (
                  <StepFileTransferEditor
                    label="get"
                    transfer={s.get}
                    onChange={(get) => updateStep(i, { get })}
                  />
                ) : null}
                {kind === 'plugin' ? (
                  <StepPluginEditor plugin={s.plugin} onChange={(plugin) => updateStep(i, { plugin })} />
                ) : null}
                {kind === 'tunnel' ? (
                  <StepTunnelEditor tunnel={s.tunnel} onChange={(tunnel) => updateStep(i, { tunnel })} />
                ) : null}
                {kind === 'agent_transfer' ? (
                  <StepAgentTransferEditor
                    at={s.agent_transfer}
                    onChange={(agent_transfer) => updateStep(i, { agent_transfer })}
                  />
                ) : null}
                {kind === 'ai' ? (
                  <>
                    <label className="rcp-edit__field">
                      model
                      <Input
                        value={s.ai?.model ?? ''}
                        onChange={(e) => updateStep(i, { ai: { ...(s.ai ?? {}), model: e.target.value } })}
                      />
                    </label>
                    <label className="rcp-edit__field rcp-edit__field--multiline">
                      prompt
                      <Input.TextArea
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
                      <Input.TextArea
                        value={s.template?.template ?? ''}
                        rows={Math.min(16, Math.max(3, (s.template?.template ?? '').split('\n').length))}
                        onChange={(e) => updateTemplate(i, { template: e.target.value })}
                      />
                    </label>
                    <label className="rcp-edit__field rcp-edit__field--multiline">
                      data (JSON object)
                      <Input.TextArea
                        value={dataJson}
                        rows={6}
                        spellCheck={false}
                        onChange={(e) => commitTemplateData(i, e.target.value)}
                        onBlur={(e) => commitTemplateData(i, e.target.value)}
                      />
                    </label>
                    <label className="rcp-edit__field">
                      output (capture variable name)
                      <Input
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
            {stepSupportsNotify(kind) ? (
              <StepNotifyEditor notify={s.notify} onChange={(notify) => updateStep(i, { notify })} />
            ) : null}
            {stepSupportsRetry(kind) ? (
              <StepRetryEditor retry={s.retry} onChange={(retry) => updateStep(i, { retry })} />
            ) : null}
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
            <Input
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
            <Input
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
            <Input
              value={Object.keys(ref.map)[0] ?? ''}
              onChange={(e) => {
                const k = e.target.value || 'VAR';
                updateRef(idx, { map: { [k]: 'stdout' } });
              }}
            />
          </label>
          <Button size="small" onClick={() => removeRef(idx)}>
            remove
          </Button>
        </div>
      ))}
      <Button size="small" onClick={addRef}>
        add env_from
      </Button>
    </fieldset>
  );
}
