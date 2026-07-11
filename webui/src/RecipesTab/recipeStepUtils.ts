import type { ParsedRecipe, ParsedRecipeStep } from '../api/types/recipes';
import { isGraphRecipe } from '../api/recipes';

export type RecipeStepKind =
  | 'command'
  | 'script'
  | 'ai'
  | 'summarize'
  | 'template'
  | 'plugin'
  | 'put'
  | 'get'
  | 'tunnel'
  | 'agent_transfer';

export const EDITABLE_KINDS = new Set<string>([
  'command',
  'script',
  'ai',
  'summarize',
  'template',
  'plugin',
  'put',
  'get',
  'tunnel',
  'agent_transfer',
]);

export const RETRY_KINDS = new Set(['command', 'script', 'plugin', 'put', 'get', 'tunnel']);

export const NOTIFY_KINDS = new Set(['command', 'script', 'plugin', 'ai', 'summarize']);

/** Cloud providers for agent_transfer steps (matches files UI / cloudtransfer). */
export const AGENT_TRANSFER_CLOUD_PROVIDERS = ['s3', 'googlecloudstorage'] as const;
export type AgentTransferCloudProvider = (typeof AGENT_TRANSFER_CLOUD_PROVIDERS)[number];

export const ADD_STEP_KINDS: RecipeStepKind[] = [
  'command',
  'script',
  'plugin',
  'put',
  'get',
  'tunnel',
  'agent_transfer',
  'ai',
  'summarize',
  'template',
];

/** Primary step action; notify is never a primary kind. */
export function stepKind(s: ParsedRecipeStep): string {
  if (s.command !== undefined) return 'command';
  if (s.script != null) return 'script';
  if (s.summarize != null) return 'summarize';
  if (s.ai != null) return 'ai';
  if (s.template != null) return 'template';
  if (s.plugin != null) return 'plugin';
  if (s.put != null) return 'put';
  if (s.get != null) return 'get';
  if (s.tunnel != null) return 'tunnel';
  if (s.agent_transfer != null) return 'agent_transfer';
  return 'unknown';
}

export function stepSupportsRetry(kind: string): boolean {
  return RETRY_KINDS.has(kind);
}

export function stepSupportsNotify(kind: string): boolean {
  return NOTIFY_KINDS.has(kind);
}

export function canAddStepKind(kind: RecipeStepKind, recipe: ParsedRecipe): boolean {
  if (kind === 'summarize' && recipe.steps.length > 0) {
    const hasSummarize = recipe.steps.some((st) => st.summarize != null);
    if (hasSummarize) return false;
  }
  return true;
}

export function defaultStepForKind(
  kind: RecipeStepKind,
  opts?: { graph?: boolean; stepNumber?: number },
): ParsedRecipeStep {
  const n = opts?.stepNumber ?? 1;
  const base: ParsedRecipeStep = { host: '*' };
  if (opts?.graph) {
    base.id = `step_${n}`;
    base.depends = [];
  }
  switch (kind) {
    case 'command':
      return { ...base, command: 'echo ok' };
    case 'script':
      return { ...base, script: { local: 'scripts/setup.sh', remote: '/tmp/setup.sh' } };
    case 'ai':
      return {
        ...base,
        host: '_',
        ai: { prompt: 'Summarize the prior step output.' },
      };
    case 'summarize':
      return {
        ...base,
        host: '_',
        summarize: { prompt: 'Summarize the run.' },
      };
    case 'template':
      return {
        ...base,
        host: '_',
        template: { template: '{{.Host}}', output: 'RESULT' },
      };
    case 'plugin':
      return { ...base, plugin: { id: 'echo', action: 'noop' } };
    case 'put':
      return { ...base, put: { local: '', remote: '' } };
    case 'get':
      return { ...base, get: { local: '', remote: '' } };
    case 'tunnel':
      return {
        ...base,
        tunnel: { remote_host: 'localhost', remote_port: 5432 },
      };
    case 'agent_transfer':
      return {
        ...base,
        agent_transfer: {
          dest_host: '*',
          source_path: '',
          dest_path: '',
          cloud: { provider: 's3', bucket: '' },
        },
      };
    default:
      return { ...base, command: '' };
  }
}

export function appendRecipeStep(recipe: ParsedRecipe, kind: RecipeStepKind): ParsedRecipe {
  const step = defaultStepForKind(kind, {
    graph: isGraphRecipe(recipe),
    stepNumber: recipe.steps.length + 1,
  });
  return { ...recipe, steps: [...recipe.steps, step] };
}

export function parseDependsText(text: string): string[] {
  return text
    .split(/[\s,]+/)
    .map((s) => s.trim())
    .filter(Boolean);
}

export function dependsText(depends: string[] | undefined): string {
  return depends?.join(', ') ?? '';
}

export function parseOptionalInt(raw: string): number | undefined {
  const trimmed = raw.trim();
  if (trimmed === '') return undefined;
  const n = Number(trimmed);
  if (!Number.isFinite(n)) return undefined;
  return Math.trunc(n);
}
