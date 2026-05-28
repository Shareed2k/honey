import { useState } from 'react';
import { Button, Checkbox, Input, InputNumber, Select } from 'antd';
import type {
  ParsedRecipeAgentTransfer,
  ParsedRecipeFileTransfer,
  ParsedRecipeNotify,
  ParsedRecipePlugin,
  ParsedRecipeStep,
  ParsedRecipeStepRetry,
  ParsedRecipeTunnel,
} from '../api';
import {
  AGENT_TRANSFER_CLOUD_PROVIDERS,
  dependsText,
  parseDependsText,
  parseOptionalInt,
} from './recipeStepUtils';

export const DEFAULT_STEP_RETRY: ParsedRecipeStepRetry = {
  attempts: 3,
  delay_ms: 1000,
  max_delay_ms: 30000,
  backoff: 'fixed',
};

export function GraphStepFields({
  step,
  onChange,
}: {
  step: ParsedRecipeStep;
  onChange: (patch: Partial<ParsedRecipeStep>) => void;
}) {
  return (
    <>
      <label className="rcp-edit__field">
        id
        <Input value={step.id ?? ''} onChange={(e) => onChange({ id: e.target.value || undefined })} />
      </label>
      <label className="rcp-edit__field">
        depends (comma-separated)
        <Input
          value={dependsText(step.depends)}
          onChange={(e) => {
            const deps = parseDependsText(e.target.value);
            onChange({ depends: deps.length ? deps : undefined });
          }}
        />
      </label>
    </>
  );
}

export function HostField({
  step,
  kind,
  onChange,
}: {
  step: ParsedRecipeStep;
  kind: string;
  onChange: (patch: Partial<ParsedRecipeStep>) => void;
}) {
  if (kind === 'template' || kind === 'ai') {
    return (
      <label className="rcp-edit__field">
        host
        <Input
          value={step.host ?? '_'}
          onChange={(e) => onChange({ host: e.target.value || '_' })}
          title='Use "_" for local/single; "*" or host name for remote targets'
        />
      </label>
    );
  }
  return (
    <label className="rcp-edit__field">
      host
      <Input value={step.host ?? ''} onChange={(e) => onChange({ host: e.target.value })} />
    </label>
  );
}

export function RunAsField({
  step,
  onChange,
}: {
  step: ParsedRecipeStep;
  onChange: (patch: Partial<ParsedRecipeStep>) => void;
}) {
  return (
    <label className="rcp-edit__field">
      run_as
      <Input
        value={step.run_as ?? ''}
        onChange={(e) => onChange({ run_as: e.target.value || undefined })}
      />
    </label>
  );
}

export function StepCommandEditor({
  step,
  onChange,
}: {
  step: ParsedRecipeStep;
  onChange: (patch: Partial<ParsedRecipeStep>) => void;
}) {
  return (
    <label className="rcp-edit__field rcp-edit__field--multiline">
      command
      <Input.TextArea
        value={step.command ?? ''}
        rows={Math.min(12, Math.max(2, (step.command ?? '').split('\n').length))}
        onChange={(e) => onChange({ command: e.target.value })}
      />
    </label>
  );
}

export function StepScriptEditor({
  script,
  onChange,
}: {
  script?: ParsedRecipeFileTransfer;
  onChange: (script: ParsedRecipeFileTransfer) => void;
}) {
  const s = script ?? { local: '', remote: '' };
  return (
    <>
      <label className="rcp-edit__field">
        local path
        <Input value={s.local ?? ''} onChange={(e) => onChange({ ...s, local: e.target.value })} />
      </label>
      <label className="rcp-edit__field">
        remote path
        <Input value={s.remote ?? ''} onChange={(e) => onChange({ ...s, remote: e.target.value })} />
      </label>
      <label className="rcp-edit__field rcp-edit__field--multiline">
        body (optional inline script)
        <Input.TextArea
          value={s.body ?? ''}
          rows={Math.min(16, Math.max(3, (s.body ?? '').split('\n').length))}
          onChange={(e) => onChange({ ...s, body: e.target.value || undefined })}
        />
      </label>
    </>
  );
}

export function StepFileTransferEditor({
  label,
  transfer,
  onChange,
}: {
  label: 'put' | 'get';
  transfer?: ParsedRecipeFileTransfer;
  onChange: (t: ParsedRecipeFileTransfer) => void;
}) {
  const t = transfer ?? { local: '', remote: '' };
  return (
    <>
      <label className="rcp-edit__field">
        local
        <Input value={t.local ?? ''} onChange={(e) => onChange({ ...t, local: e.target.value })} />
      </label>
      <label className="rcp-edit__field">
        remote
        <Input value={t.remote ?? ''} onChange={(e) => onChange({ ...t, remote: e.target.value })} />
      </label>
      {label === 'put' ? (
        <label className="rcp-edit__field rcp-edit__field--multiline">
          body (optional)
          <Input.TextArea
            value={t.body ?? ''}
            rows={4}
            onChange={(e) => onChange({ ...t, body: e.target.value || undefined })}
          />
        </label>
      ) : null}
    </>
  );
}

export function StepPluginEditor({
  plugin,
  onChange,
}: {
  plugin?: ParsedRecipePlugin;
  onChange: (plugin: ParsedRecipePlugin) => void;
}) {
  const p = plugin ?? { id: '', action: '' };
  const [configJson, setConfigJson] = useState(() => pluginConfigJson(p.config));

  function applyConfig(raw: string) {
    setConfigJson(raw);
    const trimmed = raw.trim();
    if (trimmed === '') {
      onChange({ ...p, config: undefined });
      return;
    }
    try {
      const parsed = JSON.parse(trimmed) as Record<string, unknown>;
      if (parsed !== null && typeof parsed === 'object' && !Array.isArray(parsed)) {
        onChange({ ...p, config: parsed });
      }
    } catch {
      // keep last valid until JSON parses
    }
  }

  return (
    <>
      <label className="rcp-edit__field">
        plugin id
        <Input value={p.id ?? ''} onChange={(e) => onChange({ ...p, id: e.target.value })} />
      </label>
      <label className="rcp-edit__field">
        action
        <Input value={p.action ?? ''} onChange={(e) => onChange({ ...p, action: e.target.value })} />
      </label>
      <label className="rcp-edit__field rcp-edit__field--multiline">
        config (JSON object)
        <Input.TextArea
          value={configJson}
          rows={6}
          spellCheck={false}
          onChange={(e) => applyConfig(e.target.value)}
          onBlur={(e) => applyConfig(e.target.value)}
        />
      </label>
    </>
  );
}

function pluginConfigJson(config: Record<string, unknown> | undefined): string {
  if (!config || Object.keys(config).length === 0) return '';
  try {
    return JSON.stringify(config, null, 2);
  } catch {
    return '';
  }
}

export function StepTunnelEditor({
  tunnel,
  onChange,
}: {
  tunnel?: ParsedRecipeTunnel;
  onChange: (tunnel: ParsedRecipeTunnel) => void;
}) {
  const t = tunnel ?? { remote_host: 'localhost', remote_port: 5432 };
  function patch(p: Partial<ParsedRecipeTunnel>) {
    onChange({ ...t, ...p });
  }
  return (
    <>
      <label className="rcp-edit__field">
        mode
        <Select
          value={t.mode ?? 'local'}
          onChange={(val) => patch({ mode: val || undefined })}
          options={[
            { value: 'local', label: 'local' },
            { value: 'remote', label: 'remote' },
            { value: 'dynamic', label: 'dynamic' },
          ]}
        />
      </label>
      <label className="rcp-edit__field">
        remote_host
        <Input
          value={t.remote_host ?? ''}
          onChange={(e) => patch({ remote_host: e.target.value || undefined })}
        />
      </label>
      <label className="rcp-edit__field">
        remote_port
        <InputNumber
          value={t.remote_port ?? undefined}
          onChange={(val) => patch({ remote_port: val ?? undefined })}
        />
      </label>
      <label className="rcp-edit__field">
        local_port
        <InputNumber
          value={t.local_port ?? undefined}
          onChange={(val) => patch({ local_port: val ?? undefined })}
        />
      </label>
      <Checkbox
        checked={t.use_ssh_config ?? false}
        onChange={(e) => patch({ use_ssh_config: e.target.checked || undefined })}
      >
        use_ssh_config
      </Checkbox>
      <details className="rcp-edit__details">
        <summary>Advanced tunnel options</summary>
        <label className="rcp-edit__field">
          bind
          <Input value={t.bind ?? ''} onChange={(e) => patch({ bind: e.target.value || undefined })} />
        </label>
        <label className="rcp-edit__field">
          ssh_config_match
          <Input
            value={t.ssh_config_match ?? ''}
            onChange={(e) => patch({ ssh_config_match: e.target.value || undefined })}
          />
        </label>
        <label className="rcp-edit__field">
          share_key
          <Input value={t.share_key ?? ''} onChange={(e) => patch({ share_key: e.target.value || undefined })} />
        </label>
      </details>
    </>
  );
}

export function StepAgentTransferEditor({
  at,
  onChange,
}: {
  at?: ParsedRecipeAgentTransfer;
  onChange: (at: ParsedRecipeAgentTransfer) => void;
}) {
  const a = at ?? {
    dest_host: '*',
    source_path: '',
    dest_path: '',
    cloud: { provider: 's3', bucket: '' },
  };
  function patch(p: Partial<ParsedRecipeAgentTransfer>) {
    onChange({ ...a, ...p });
  }
  function patchCloud(p: Partial<ParsedRecipeAgentTransfer['cloud']>) {
    onChange({ ...a, cloud: { ...a.cloud, ...p } });
  }
  const cloudProvider = a.cloud?.provider ?? 's3';
  const knownProviders: readonly string[] = AGENT_TRANSFER_CLOUD_PROVIDERS;
  const providerOptions = [
    ...AGENT_TRANSFER_CLOUD_PROVIDERS.map((p) => ({ value: p, label: p })),
    ...(!knownProviders.includes(cloudProvider)
      ? [{ value: cloudProvider, label: cloudProvider }]
      : []),
  ];
  return (
    <>
      <label className="rcp-edit__field">
        dest_host
        <Input value={a.dest_host ?? ''} onChange={(e) => patch({ dest_host: e.target.value })} />
      </label>
      <label className="rcp-edit__field">
        source_path
        <Input value={a.source_path ?? ''} onChange={(e) => patch({ source_path: e.target.value })} />
      </label>
      <label className="rcp-edit__field">
        dest_path
        <Input value={a.dest_path ?? ''} onChange={(e) => patch({ dest_path: e.target.value })} />
      </label>
      <fieldset className="rcp-edit__cloud">
        <legend>cloud</legend>
        <label className="rcp-edit__field">
          provider
          <Select
            value={cloudProvider}
            onChange={(val) => patchCloud({ provider: val })}
            options={providerOptions}
          />
        </label>
        <label className="rcp-edit__field">
          bucket
          <Input value={a.cloud?.bucket ?? ''} onChange={(e) => patchCloud({ bucket: e.target.value })} />
        </label>
        <label className="rcp-edit__field">
          prefix
          <Input
            value={a.cloud?.prefix ?? ''}
            onChange={(e) => patchCloud({ prefix: e.target.value || undefined })}
          />
        </label>
        <label className="rcp-edit__field">
          region
          <Input
            value={a.cloud?.region ?? ''}
            onChange={(e) => patchCloud({ region: e.target.value || undefined })}
          />
        </label>
      </fieldset>
      <Checkbox
        checked={a.keep_object ?? false}
        onChange={(e) => patch({ keep_object: e.target.checked || undefined })}
      >
        keep_object
      </Checkbox>
    </>
  );
}

export function StepNotifyEditor({
  notify,
  onChange,
}: {
  notify?: ParsedRecipeNotify;
  onChange: (notify: ParsedRecipeNotify | undefined) => void;
}) {
  const enabled = notify != null;
  const n = notify ?? {};

  function patch(p: Partial<ParsedRecipeNotify>) {
    onChange({ ...n, ...p });
  }

  return (
    <fieldset className="rcp-edit__notify">
      <legend>
        <Checkbox
          checked={enabled}
          onChange={(e) => onChange(e.target.checked ? {} : undefined)}
        >
          notify
        </Checkbox>
      </legend>
      {enabled ? (
        <>
          <label className="rcp-edit__field">
            notify_subject
            <Input
              value={n.notify_subject ?? ''}
              onChange={(e) => patch({ notify_subject: e.target.value || undefined })}
            />
          </label>
          <label className="rcp-edit__field rcp-edit__field--multiline">
            message
            <Input.TextArea
              value={n.message ?? ''}
              rows={3}
              onChange={(e) => patch({ message: e.target.value || undefined })}
            />
          </label>
          <p className="rcp-edit__hint">
            <code>services</code> (http, slack, telegram) are preserved from the recipe file; edit in CUE for
            full control.
          </p>
        </>
      ) : null}
    </fieldset>
  );
}

export function StepRetryEditor({
  retry,
  onChange,
}: {
  retry?: ParsedRecipeStepRetry;
  onChange: (retry: ParsedRecipeStepRetry | undefined) => void;
}) {
  const enabled = retry != null;
  const cfg = retry ?? DEFAULT_STEP_RETRY;

  function patch(p: Partial<ParsedRecipeStepRetry>) {
    onChange({ ...cfg, ...p });
  }

  return (
    <fieldset className="rcp-edit__retry">
      <legend>
        <Checkbox
          checked={enabled}
          onChange={(e) => onChange(e.target.checked ? { ...DEFAULT_STEP_RETRY } : undefined)}
        >
          retry
        </Checkbox>
      </legend>
      {enabled ? (
        <>
          <p className="rcp-edit__hint">
            Re-runs failed per-host actions. <code>attempts</code> is total tries (use ≥ 2 to retry). Skipped{' '}
            <code>when</code> targets are not retried.
          </p>
          <label className="rcp-edit__field">
            attempts
            <InputNumber
              min={1}
              value={cfg.attempts ?? undefined}
              onChange={(val) => {
                patch({ attempts: val !== null && val >= 1 ? val : undefined });
              }}
            />
          </label>
          <label className="rcp-edit__field">
            delay_ms
            <InputNumber
              min={0}
              value={cfg.delay_ms ?? undefined}
              onChange={(val) => {
                patch({ delay_ms: val !== null && val >= 0 ? val : undefined });
              }}
            />
          </label>
          <label className="rcp-edit__field">
            max_delay_ms
            <InputNumber
              min={0}
              value={cfg.max_delay_ms ?? undefined}
              onChange={(val) => {
                patch({ max_delay_ms: val !== null && val >= 0 ? val : undefined });
              }}
            />
          </label>
          <label className="rcp-edit__field">
            backoff
            <Select
              value={cfg.backoff ?? 'fixed'}
              onChange={(val) =>
                patch({
                  backoff: val === 'exponential' ? 'exponential' : 'fixed',
                })
              }
              options={[
                { value: 'fixed', label: 'fixed' },
                { value: 'exponential', label: 'exponential' },
              ]}
            />
          </label>
        </>
      ) : null}
    </fieldset>
  );
}
