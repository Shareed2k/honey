// webui/src/RecipesTab/StepPlan.tsx
import { useCallback, useEffect, useState } from 'react';
import { Alert, Button, Checkbox, Collapse, Input, Modal, Spin, Tabs, Typography } from 'antd';
import {
  isGraphRecipe,
  validateRecipeContent,
  type ParsedRecipe,
  type RecipeGraphPlan,
  type ResolvedStep,
  type ValidationError,
  type RiskReport,
} from '../api';
import type { EnvPair, PlanState } from './types';
import { EditForm } from './EditForm';
import { RecipeGraphFlow } from './RecipeGraphFlow';
import { ParameterPromptModal } from '../RecipeStudio/ParameterPromptModal';

type PlanTab = 'plan' | 'graph' | 'edit';

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
  sessionRecordingAvailable: boolean;
  hostCount: number;
  onBack: () => void;
  onExecute: () => void;
  validationRisk?: RiskReport;
};

export function StepPlan(props: Props) {
  const graphMode = isGraphRecipe(props.recipe);
  const [tab, setTab] = useState<PlanTab>('plan');
  const [plan, setPlan] = useState<PlanState>(null);
  const [promptsOpen, setPromptsOpen] = useState(false);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      const res = await validateRecipeContent(props.recipe);
      if (cancelled) return;
      setPlan(
        'errors' in res
          ? { ok: false, errors: res.errors }
          : { ok: true, plan: res.plan, steps: res.steps, graph: res.graph, risk: res.risk },
      );
    })();
    return () => {
      cancelled = true;
    };
  }, [props.recipe]);

  const handleEditErrors = useCallback((errors: ValidationError[]) => {
    setPlan({ ok: false, errors });
  }, []);

  const handleEditValidated = useCallback(
    (res: { plan: string; steps: ResolvedStep[]; graph?: RecipeGraphPlan }) => {
      setPlan({ ok: true, plan: res.plan, steps: res.steps, graph: res.graph });
    },
    [],
  );

  const hasErrors = plan && !plan.ok;
  const root = props.recipe.steps.some((s) => s.run_as === 'root');
  const dangerous = root || !props.recordSession;

  function handleExecute() {
    const prompts = props.baseRecipe?.defaults?.prompts;
    if (prompts && Object.keys(prompts).length > 0) {
      setPromptsOpen(true);
      return;
    }

    const msg = dangerous
      ? 'Execute this recipe? Some steps run as root and/or session recording is off.'
      : `Execute this recipe on ${props.hostCount} host${props.hostCount === 1 ? '' : 's'}?`;
    Modal.confirm({
      title: 'Execute recipe',
      content: msg,
      okText: 'Execute',
      okButtonProps: { danger: dangerous },
      onOk: props.onExecute,
    });
  }

  const tabItems = [
    { key: 'plan', label: 'Plan' },
    ...(graphMode ? [{ key: 'graph', label: 'Graph' }] : []),
    { key: 'edit', label: 'Edit' },
  ];

  return (
    <div className="rcp-step rcp-step--plan">
      <header className="rcp-step__header">
        <Typography.Title level={5} style={{ margin: 0 }}>③ Review plan</Typography.Title>
        <Tabs
          activeKey={tab}
          onChange={(key) => setTab(key as PlanTab)}
          items={tabItems}
          size="small"
        />
      </header>

      {tab === 'plan' ? (
        <PlanView plan={plan} graphMode={graphMode} />
      ) : tab === 'graph' ? (
        <GraphView plan={plan} />
      ) : (
        <EditForm
          recipe={props.recipe}
          baseRecipe={props.baseRecipe}
          onChange={props.onRecipeChange}
          onErrors={handleEditErrors}
          onValidated={handleEditValidated}
          onSaveAsDraft={props.onSaveAsDraft}
        />
      )}

      <section className="rcp-controls">
        <Collapse
          size="small"
          ghost
          items={[{
            key: 'env',
            label: `Env overrides (${props.envOverrides.length})`,
            children: <EnvEditor pairs={props.envOverrides} onChange={props.onEnvChange} />,
          }]}
        />
        <label className="rcp-field">
          ssh_user
          <Input
            value={props.sshUser}
            onChange={(e) => props.onSSHUserChange(e.target.value)}
          />
        </label>
        <div className="rcp-field rcp-field--checkbox">
          <Checkbox
            checked={props.recordSession}
            disabled={!props.sessionRecordingAvailable}
            onChange={(e) => props.onRecordSessionChange(e.target.checked)}
          >
            record session
          </Checkbox>
        </div>
        {props.recordSession && !props.sessionRecordingAvailable ? (
          <Alert
            type="warning"
            message={
              <>
                Session recording is not available on this server (start{' '}
                <Typography.Text code>honey web</Typography.Text> with{' '}
                <Typography.Text code>--record-dir</Typography.Text> or set{' '}
                <Typography.Text code>defaults.record_dir</Typography.Text>).
              </>
            }
          />
        ) : null}
        
        {(() => {
          const risk = props.validationRisk || (plan?.ok ? plan.risk : undefined);
          return risk && risk.score > 0 ? (
            <div style={{ marginBottom: 16, marginTop: 16 }}>
              <Alert
                type={risk.level === 'High' ? 'error' : risk.level === 'Medium' ? 'warning' : 'info'}
                showIcon
                message={`Risk Level: ${risk.level} (Score: ${risk.score})`}
                description={
                  <ul style={{ margin: 0, paddingLeft: 20, marginTop: 8 }}>
                    {risk.findings.map((f: string, i: number) => <li key={i}>{f}</li>)}
                  </ul>
                }
              />
            </div>
          ) : null;
        })()}
      </section>

      <footer className="rcp-step__footer">
        <Button onClick={props.onBack}>
          ← back
        </Button>
        <Button
          danger
          type="primary"
          disabled={!!hasErrors}
          onClick={handleExecute}
        >
          Execute on {props.hostCount} host{props.hostCount === 1 ? '' : 's'} ▶
        </Button>
      </footer>
      <ParameterPromptModal
        open={promptsOpen}
        prompts={props.baseRecipe?.defaults?.prompts || {}}
        onCancel={() => setPromptsOpen(false)}
        onSubmit={(vals) => {
          setPromptsOpen(false);
          const newEnv = Object.entries(vals).map(([k, v]) => ({ key: k, value: v }));
          props.onEnvChange([...props.envOverrides, ...newEnv]);
          setTimeout(() => {
            props.onExecute();
          }, 50);
        }}
      />
    </div>
  );
}

function PlanView({ plan, graphMode }: { plan: PlanState; graphMode: boolean }) {
  if (!plan) return <Spin tip="Resolving plan…"><div style={{ minHeight: 40 }} /></Spin>;
  if (!plan.ok) {
    return (
      <Alert
        type="error"
        message={
          <>
            <strong>
              {plan.errors.length} issue{plan.errors.length === 1 ? '' : 's'}:
            </strong>
            <ul>
              {plan.errors.map((e, i) => (
                <li key={i}>
                  {e.path ? `${e.path}: ` : ''}
                  {e.message}
                </li>
              ))}
            </ul>
          </>
        }
      />
    );
  }
  return (
    <ul className={'rcp-steps' + (graphMode ? ' rcp-steps--graph' : '')}>
      {plan.steps.map((s: ResolvedStep) => (
        <li key={s.index}>
          <span className="rcp-steps__idx">{s.index + 1}</span>
          {s.id ? <span className="rcp-steps__id">{s.id}</span> : null}
          <span className="rcp-steps__kind">{s.kind}</span>
          {s.wave ? <span className="rcp-steps__wave">w{s.wave}</span> : null}
          {s.run_as ? <span className="rcp-steps__runas">run_as={s.run_as}</span> : null}
          {s.when ? <span className="rcp-steps__tag">when={s.when}</span> : null}
          {s.retry ? <span className="rcp-steps__tag">retry={s.retry}</span> : null}
          {s.notify ? <span className="rcp-steps__tag rcp-steps__tag--notify">notify</span> : null}
          <span className="rcp-steps__host">host={s.host}</span>
          <code className="rcp-steps__preview">{s.preview}</code>
        </li>
      ))}
    </ul>
  );
}

function GraphView({ plan }: { plan: PlanState }) {
  if (!plan) return <Spin tip="Resolving graph…"><div style={{ minHeight: 40 }} /></Spin>;
  if (!plan.ok) {
    return <Alert type="error" message="Fix validation errors before viewing the graph." />;
  }
  if (!plan.graph) {
    return <Alert type="error" message="No graph data returned for this recipe." />;
  }
  return <RecipeGraphFlow plan={plan.graph} />;
}

function EnvEditor({ pairs, onChange }: { pairs: EnvPair[]; onChange: (e: EnvPair[]) => void }) {
  return (
    <div className="rcp-env__rows">
      {pairs.map((p, i) => (
        <div key={i} className="rcp-env__row">
          <Input
            value={p.key}
            placeholder="KEY"
            onChange={(e) => {
              const next = [...pairs];
              next[i] = { ...next[i], key: e.target.value };
              onChange(next);
            }}
          />
          <Input
            value={p.value}
            placeholder="value"
            onChange={(e) => {
              const next = [...pairs];
              next[i] = { ...next[i], value: e.target.value };
              onChange(next);
            }}
          />
          <Button type="text" onClick={() => onChange(pairs.filter((_, j) => j !== i))}>
            ×
          </Button>
        </div>
      ))}
      <Button type="dashed" onClick={() => onChange([...pairs, { key: '', value: '' }])}>
        + add
      </Button>
    </div>
  );
}
