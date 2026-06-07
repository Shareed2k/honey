export type StepKindOption = {
  kind: string;
  label: string;
};

export type StepDraft = Record<string, unknown> & {
  id: string;
  kind: string;
  host: string;
};

type FlowNodeLike = { id: string };
type FlowEdgeLike = { source: string; target: string };
type PositionedNode = {
  id: string;
  position: { x: number; y: number };
  data?: { wave?: number } & Record<string, unknown>;
};

const WAVE_COL_W = 240;
const WAVE_ROW_H = 110;
const WAVE_X0 = 100;
const WAVE_Y0 = 80;

const preferredKindOrder = [
  'command',
  'script',
  'put',
  'get',
  'template',
  'plugin',
  'tunnel',
  'ai',
  'agent_transfer',
  'docker',
  'k8s',
  'postgres',
  'opensearch',
];

const kindLabels: Record<string, string> = {
  ai: 'AI',
  k8s: 'Kubernetes',
  postgres: 'Postgres',
  opensearch: 'OpenSearch',
  agent_transfer: 'Agent Transfer',
};

// listStepKinds reads the per-kind definitions emitted by BuildRecipeStepJSONSchema
// (definitions: { command: {...}, postgres: {...}, ..., defaults: {...} }). "defaults"
// is the recipe-level block, not a step kind, so it is excluded.
// eslint-disable-next-line @typescript-eslint/no-explicit-any
export function listStepKinds(schema: any): StepKindOption[] {
  const names = new Set<string>();

  for (const key of Object.keys(schema?.definitions ?? {})) {
    if (key !== 'defaults') {
      names.add(key);
    }
  }

  return [...names]
    .sort((a, b) => kindSortIndex(a) - kindSortIndex(b) || a.localeCompare(b))
    .map((kind) => ({ kind, label: kindLabels[kind] || titleCase(kind) }));
}

// resolveRefs inlines all local $ref ("#/$defs/Name" or "#/definitions/Name") using
// the schema's own $defs/definitions table, so the form (which does not resolve refs)
// can render nested object fields. Cycles are broken by tracking visited refs.
// eslint-disable-next-line @typescript-eslint/no-explicit-any
function resolveRefs(node: any, defs: Record<string, any>, seen: Set<string> = new Set()): any {
  if (Array.isArray(node)) {
    return node.map((n) => resolveRefs(n, defs, seen));
  }
  if (!node || typeof node !== 'object') {
    return node;
  }
  if (typeof node.$ref === 'string') {
    const name = node.$ref.replace(/^#\/(\$defs|definitions)\//, '');
    if (seen.has(name) || !defs[name]) {
      return { type: 'object' };
    }
    const next = new Set(seen);
    next.add(name);
    return resolveRefs(defs[name], defs, next);
  }
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const out: any = {};
  for (const [k, v] of Object.entries(node)) {
    if (k === '$defs' || k === 'definitions') {
      continue;
    }
    out[k] = resolveRefs(v, defs, seen);
  }
  return out;
}

export function detectStepKind(step: Record<string, unknown>): string {
  for (const kind of preferredKindOrder) {
    const value = step[kind];
    if (value !== undefined && value !== null && value !== '') {
      return kind;
    }
  }
  return 'command';
}

export function createStepDraft(kind: string, id: string): StepDraft {
  const draft: StepDraft = { id, kind, host: '*' };
  if (kind === 'command') {
    draft.command = '';
  } else {
    draft[kind] = {};
  }
  return draft;
}

// stepSchemaForKind returns the self-contained, ref-resolved object schema for one
// step kind (or "defaults"). definitions[kind] is the full reflected struct schema
// for that kind; its internal $ref entries are inlined so the form can render them.
// eslint-disable-next-line @typescript-eslint/no-explicit-any
export function stepSchemaForKind(schema: any, kind?: string): any {
  if (!kind || !schema?.definitions?.[kind]) {
    return schema;
  }
  const def = schema.definitions[kind];
  const defs = def.$defs ?? def.definitions ?? {};
  return resolveRefs(def, defs);
}

export function buildRecipeFromFlow(input: {
  name: string;
  nodes: FlowNodeLike[];
  edges: FlowEdgeLike[];
  stepData: Record<string, StepDraft>;
}): { name: string; type: string; steps: Record<string, unknown>[] } {
  const steps = input.nodes.map((node) => {
    const data = input.stepData[node.id] || createStepDraft('command', node.id);
    const deps = input.edges.filter((edge) => edge.target === node.id).map((edge) => edge.source).filter(Boolean);
    const stepObj: Record<string, unknown> = {};

    for (const [key, value] of Object.entries(data)) {
      if (key === 'kind' || value === undefined || value === null || value === '') {
        continue;
      }
      if (Array.isArray(value) && value.length === 0) {
        continue;
      }
      if (isEmptyObject(value) && key !== data.kind) {
        continue;
      }
      stepObj[key] = value;
    }

    if (deps.length > 0) {
      stepObj.depends = deps;
    }

    return stepObj;
  });

  return { name: input.name, type: 'graph', steps };
}

export function recipeNameFromFilename(fileName?: string): string {
  const baseName = (fileName || '').split('/').pop()?.trim() || 'visual-studio-recipe';
  return baseName.replace(/\.cue$/i, '') || 'visual-studio-recipe';
}

export function applyWaveLayout<T extends PositionedNode>(nodes: T[]): T[] {
  const knownWaves = nodes
    .map((node) => node.data?.wave)
    .filter((wave): wave is number => typeof wave === 'number' && Number.isFinite(wave));
  const fallbackWave = knownWaves.length > 0 ? Math.max(...knownWaves) + 1 : 1;
  const rowByWave = new Map<number, number>();

  return nodes.map((node) => {
    const wave = typeof node.data?.wave === 'number' && Number.isFinite(node.data.wave) ? node.data.wave : fallbackWave;
    const row = rowByWave.get(wave) || 0;
    rowByWave.set(wave, row + 1);

    return {
      ...node,
      position: {
        x: WAVE_X0 + (wave - 1) * WAVE_COL_W,
        y: WAVE_Y0 + row * WAVE_ROW_H,
      },
    };
  });
}

function kindSortIndex(kind: string): number {
  const index = preferredKindOrder.indexOf(kind);
  return index === -1 ? preferredKindOrder.length : index;
}

function titleCase(value: string): string {
  return value
    .split('_')
    .map((part) => part.charAt(0).toUpperCase() + part.slice(1))
    .join(' ');
}

function isEmptyObject(value: unknown): boolean {
  return typeof value === 'object' && value !== null && !Array.isArray(value) && Object.keys(value).length === 0;
}
