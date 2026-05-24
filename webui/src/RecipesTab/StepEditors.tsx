import { useState } from 'react';
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
        <input value={step.id ?? ''} onChange={(e) => onChange({ id: e.target.value || undefined })} />
      </label>
      <label className="rcp-edit__field">
        depends (comma-separated)
        <input
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
        <input
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
      <input value={step.host ?? ''} onChange={(e) => onChange({ host: e.target.value })} />
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
      <input
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
      <textarea
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
        <input value={s.local ?? ''} onChange={(e) => onChange({ ...s, local: e.target.value })} />
      </label>
      <label className="rcp-edit__field">
        remote path
        <input value={s.remote ?? ''} onChange={(e) => onChange({ ...s, remote: e.target.value })} />
      </label>
      <label className="rcp-edit__field rcp-edit__field--multiline">
        body (optional inline script)
        <textarea
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
        <input value={t.local ?? ''} onChange={(e) => onChange({ ...t, local: e.target.value })} />
      </label>
      <label className="rcp-edit__field">
        remote
        <input value={t.remote ?? ''} onChange={(e) => onChange({ ...t, remote: e.target.value })} />
      </label>
      {label === 'put' ? (
        <label className="rcp-edit__field rcp-edit__field--multiline">
          body (optional)
          <textarea
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
        <input value={p.id ?? ''} onChange={(e) => onChange({ ...p, id: e.target.value })} />
      </label>
      <label className="rcp-edit__field">
        action
        <input value={p.action ?? ''} onChange={(e) => onChange({ ...p, action: e.target.value })} />
      </label>
      <label className="rcp-edit__field rcp-edit__field--multiline">
        config (JSON object)
        <textarea
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
        <select value={t.mode ?? 'local'} onChange={(e) => patch({ mode: e.target.value || undefined })}>
          <option value="local">local</option>
          <option value="remote">remote</option>
          <option value="dynamic">dynamic</option>
        </select>
      </label>
      <label className="rcp-edit__field">
        remote_host
        <input
          value={t.remote_host ?? ''}
          onChange={(e) => patch({ remote_host: e.target.value || undefined })}
        />
      </label>
      <label className="rcp-edit__field">
        remote_port
        <input
          type="number"
          value={t.remote_port ?? ''}
          onChange={(e) => {
            const n = parseOptionalInt(e.target.value);
            patch({ remote_port: n });
          }}
        />
      </label>
      <label className="rcp-edit__field">
        local_port
        <input
          type="number"
          value={t.local_port ?? ''}
          onChange={(e) => {
            const n = parseOptionalInt(e.target.value);
            patch({ local_port: n });
          }}
        />
      </label>
      <label className="rcp-edit__field">
        <input
          type="checkbox"
          checked={t.use_ssh_config ?? false}
          onChange={(e) => patch({ use_ssh_config: e.target.checked || undefined })}
        />{' '}
        use_ssh_config
      </label>
      <details className="rcp-edit__details">
        <summary>Advanced tunnel options</summary>
        <label className="rcp-edit__field">
          bind
          <input value={t.bind ?? ''} onChange={(e) => patch({ bind: e.target.value || undefined })} />
        </label>
        <label className="rcp-edit__field">
          ssh_config_match
          <input
            value={t.ssh_config_match ?? ''}
            onChange={(e) => patch({ ssh_config_match: e.target.value || undefined })}
          />
        </label>
        <label className="rcp-edit__field">
          share_key
          <input value={t.share_key ?? ''} onChange={(e) => patch({ share_key: e.target.value || undefined })} />
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
  return (
    <>
      <label className="rcp-edit__field">
        dest_host
        <input value={a.dest_host ?? ''} onChange={(e) => patch({ dest_host: e.target.value })} />
      </label>
      <label className="rcp-edit__field">
        source_path
        <input value={a.source_path ?? ''} onChange={(e) => patch({ source_path: e.target.value })} />
      </label>
      <label className="rcp-edit__field">
        dest_path
        <input value={a.dest_path ?? ''} onChange={(e) => patch({ dest_path: e.target.value })} />
      </label>
      <fieldset className="rcp-edit__cloud">
        <legend>cloud</legend>
        <label className="rcp-edit__field">
          provider
          <select
            value={cloudProvider}
            onChange={(e) => patchCloud({ provider: e.target.value })}
          >
            {AGENT_TRANSFER_CLOUD_PROVIDERS.map((p) => (
              <option key={p} value={p}>
                {p}
              </option>
            ))}
            {!knownProviders.includes(cloudProvider) ? (
              <option value={cloudProvider}>{cloudProvider}</option>
            ) : null}
          </select>
        </label>
        <label className="rcp-edit__field">
          bucket
          <input value={a.cloud?.bucket ?? ''} onChange={(e) => patchCloud({ bucket: e.target.value })} />
        </label>
        <label className="rcp-edit__field">
          prefix
          <input
            value={a.cloud?.prefix ?? ''}
            onChange={(e) => patchCloud({ prefix: e.target.value || undefined })}
          />
        </label>
        <label className="rcp-edit__field">
          region
          <input
            value={a.cloud?.region ?? ''}
            onChange={(e) => patchCloud({ region: e.target.value || undefined })}
          />
        </label>
      </fieldset>
      <label className="rcp-edit__field">
        <input
          type="checkbox"
          checked={a.keep_object ?? false}
          onChange={(e) => patch({ keep_object: e.target.checked || undefined })}
        />{' '}
        keep_object
      </label>
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
        <label className="rcp-edit__notify-toggle">
          <input
            type="checkbox"
            checked={enabled}
            onChange={(e) => onChange(e.target.checked ? {} : undefined)}
          />
          notify
        </label>
      </legend>
      {enabled ? (
        <>
          <label className="rcp-edit__field">
            notify_subject
            <input
              value={n.notify_subject ?? ''}
              onChange={(e) => patch({ notify_subject: e.target.value || undefined })}
            />
          </label>
          <label className="rcp-edit__field rcp-edit__field--multiline">
            message
            <textarea
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
        <label className="rcp-edit__retry-toggle">
          <input
            type="checkbox"
            aria-label="retry"
            checked={enabled}
            onChange={(e) => onChange(e.target.checked ? { ...DEFAULT_STEP_RETRY } : undefined)}
          />
          retry
        </label>
      </legend>
      {enabled ? (
        <>
          <p className="rcp-edit__hint">
            Re-runs failed per-host actions. <code>attempts</code> is total tries (use ≥ 2 to retry). Skipped{' '}
            <code>when</code> targets are not retried.
          </p>
          <label className="rcp-edit__field">
            attempts
            <input
              type="number"
              min={1}
              value={cfg.attempts ?? ''}
              onChange={(e) => {
                const n = parseOptionalInt(e.target.value);
                patch({ attempts: n !== undefined && n >= 1 ? n : undefined });
              }}
            />
          </label>
          <label className="rcp-edit__field">
            delay_ms
            <input
              type="number"
              min={0}
              value={cfg.delay_ms ?? ''}
              onChange={(e) => {
                const n = parseOptionalInt(e.target.value);
                patch({ delay_ms: n !== undefined && n >= 0 ? n : undefined });
              }}
            />
          </label>
          <label className="rcp-edit__field">
            max_delay_ms
            <input
              type="number"
              min={0}
              value={cfg.max_delay_ms ?? ''}
              onChange={(e) => {
                const n = parseOptionalInt(e.target.value);
                patch({ max_delay_ms: n !== undefined && n >= 0 ? n : undefined });
              }}
            />
          </label>
          <label className="rcp-edit__field">
            backoff
            <select
              value={cfg.backoff ?? 'fixed'}
              onChange={(e) =>
                patch({
                  backoff: e.target.value === 'exponential' ? 'exponential' : 'fixed',
                })
              }
            >
              <option value="fixed">fixed</option>
              <option value="exponential">exponential</option>
            </select>
          </label>
        </>
      ) : null}
    </fieldset>
  );
}
