// webui/src/RecipesTab/StepPlan.tsx
import { useEffect, useState } from 'react';
import { validateRecipeContent, type ParsedRecipe, type ResolvedStep } from '../api';
import type { EnvPair, PlanState } from './types';
import { EditForm } from './EditForm';

type Props = {
  recipe: ParsedRecipe;
  baseRecipe: ParsedRecipe;
  onRecipeChange: (next: ParsedRecipe) => void;
  onSaveAsDraft: (name: string) => void;
  envOverrides: EnvPair[];
  onEnvChange: (env: EnvPair[]) => void;
  sshUser: string;
  onSSHUserChange: (u: string) => void;
  recordSession: boolean;
  onRecordSessionChange: (v: boolean) => void;
  hostCount: number;
  onBack: () => void;
  onExecute: () => void;
};

export function StepPlan(props: Props) {
  const [tab, setTab] = useState<'plan' | 'edit'>('plan');
  const [plan, setPlan] = useState<PlanState>(null);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      const res = await validateRecipeContent(props.recipe);
      if (cancelled) return;
      setPlan('errors' in res ? { ok: false, errors: res.errors } : { ok: true, plan: res.plan, steps: res.steps });
    })();
    return () => { cancelled = true; };
  }, [props.recipe]);

  const hasErrors = plan && !plan.ok;
  const root = props.recipe.steps.some((s) => s.run_as === 'root');
  const dangerous = root || !props.recordSession;

  function handleExecute() {
    const msg = dangerous
      ? 'Execute this recipe? Some steps run as root and/or session recording is off.'
      : `Execute this recipe on ${props.hostCount} host${props.hostCount === 1 ? '' : 's'}?`;
    if (window.confirm(msg)) props.onExecute();
  }

  return (
    <div className="rcp-step rcp-step--plan">
      <header className="rcp-step__header">
        <h2>③ Review plan</h2>
        <div className="rcp-tabstrip">
          <button
            type="button"
            className={'rcp-tab' + (tab === 'plan' ? ' rcp-tab--active' : '')}
            onClick={() => setTab('plan')}
          >Plan</button>
          <button
            type="button"
            className={'rcp-tab' + (tab === 'edit' ? ' rcp-tab--active' : '')}
            onClick={() => setTab('edit')}
          >Edit</button>
        </div>
      </header>

      {tab === 'plan' ? (
        <PlanView plan={plan} />
      ) : (
        <EditForm
          recipe={props.recipe}
          baseRecipe={props.baseRecipe}
          onChange={props.onRecipeChange}
          onErrors={(errs) => setPlan(errs ? { ok: false, errors: errs } : null)}
          onSaveAsDraft={props.onSaveAsDraft}
        />
      )}

      <section className="rcp-controls">
        <details className="rcp-env">
          <summary>Env overrides ({props.envOverrides.length})</summary>
          <EnvEditor pairs={props.envOverrides} onChange={props.onEnvChange} />
        </details>
        <label className="rcp-field">
          ssh_user
          <input
            type="text"
            value={props.sshUser}
            onChange={(e) => props.onSSHUserChange(e.target.value)}
          />
        </label>
        <label className="rcp-field rcp-field--checkbox">
          <input
            type="checkbox"
            checked={props.recordSession}
            onChange={(e) => props.onRecordSessionChange(e.target.checked)}
          />
          record session
        </label>
      </section>

      <footer className="rcp-step__footer">
        <button type="button" className="rcp-btn" onClick={props.onBack}>← back</button>
        <button
          type="button"
          className="rcp-btn rcp-btn--danger"
          disabled={!!hasErrors}
          onClick={handleExecute}
        >
          Execute on {props.hostCount} host{props.hostCount === 1 ? '' : 's'} ▶
        </button>
      </footer>
    </div>
  );
}

function PlanView({ plan }: { plan: PlanState }) {
  if (!plan) return <div className="rcp-loading">Resolving plan…</div>;
  if (!plan.ok) {
    return (
      <div className="rcp-errors">
        <strong>{plan.errors.length} issue{plan.errors.length === 1 ? '' : 's'}:</strong>
        <ul>{plan.errors.map((e, i) => <li key={i}>{e.path ? `${e.path}: ` : ''}{e.message}</li>)}</ul>
      </div>
    );
  }
  return (
    <ul className="rcp-steps">
      {plan.steps.map((s: ResolvedStep) => (
        <li key={s.index}>
          <span className="rcp-steps__idx">{s.index + 1}</span>
          <span className="rcp-steps__kind">{s.kind}</span>
          {s.run_as ? <span className="rcp-steps__runas">run_as={s.run_as}</span> : null}
          <span className="rcp-steps__host">host={s.host}</span>
          <code className="rcp-steps__preview">{s.preview}</code>
        </li>
      ))}
    </ul>
  );
}

function EnvEditor({ pairs, onChange }: { pairs: EnvPair[]; onChange: (e: EnvPair[]) => void }) {
  return (
    <div className="rcp-env__rows">
      {pairs.map((p, i) => (
        <div key={i} className="rcp-env__row">
          <input
            value={p.key}
            placeholder="KEY"
            onChange={(e) => {
              const next = [...pairs];
              next[i] = { ...next[i], key: e.target.value };
              onChange(next);
            }}
          />
          <input
            value={p.value}
            placeholder="value"
            onChange={(e) => {
              const next = [...pairs];
              next[i] = { ...next[i], value: e.target.value };
              onChange(next);
            }}
          />
          <button type="button" onClick={() => onChange(pairs.filter((_, j) => j !== i))}>×</button>
        </div>
      ))}
      <button type="button" onClick={() => onChange([...pairs, { key: '', value: '' }])}>+ add</button>
    </div>
  );
}
