import { apiHeaders, apiGet, apiPost } from './core';
import { RecipeListEntry, ParsedRecipe, RecipeGraphPlan, ValidationError, RecipesAIGraphResponse } from './types/recipes';
import { ResolvedStep, RiskReport, LibraryCategory } from './types/core';



export function isGraphRecipe(recipe: ParsedRecipe): boolean {
  return recipe.type?.trim().toLowerCase() === 'graph';
}

export async function fetchRecipes(): Promise<RecipeListEntry[]> {
  const r = await apiGet('/api/v1/recipes');
  const j = (await r.json().catch(() => ({}))) as { recipes?: RecipeListEntry[]; error?: string };
  if (!r.ok) {
    throw new Error(j.error || r.statusText);
  }
  return j.recipes || [];
}

/**
 * Validate a structured recipe payload. Returns `{plan, steps}` on success (200), or
 * `{errors}` on a 400 validation failure. Other HTTP errors throw.
 */
export async function validateRecipeContent(
  recipe: ParsedRecipe,
): Promise<{ plan: string; steps: ResolvedStep[]; graph?: RecipeGraphPlan; risk?: RiskReport } | { errors: ValidationError[] }> {
  const r = await apiPost('/api/v1/recipes/validate-content', { recipe_content: recipe });
  const body = (await r.json().catch(() => ({}))) as {
    plan?: string;
    steps?: ResolvedStep[];
    graph?: RecipeGraphPlan;
    errors?: ValidationError[];
    error?: string;
    risk?: RiskReport;
  };
  if (r.ok) {
    return { plan: body.plan ?? '', steps: body.steps ?? [], graph: body.graph, risk: body.risk };
  }
  if (r.status === 400) {
    return {
      errors: body.errors ?? [{ kind: 'validation', message: body.error || 'unknown error' }],
    };
  }
  throw new Error(body.error || r.statusText);
}

export async function fetchRecipeContent(path: string): Promise<string> {
  const r = await apiPost('/api/v1/recipes/view', { path });
  const j = (await r.json().catch(() => ({}))) as { content?: string; error?: string };
  if (!r.ok) {
    throw new Error(j.error || r.statusText);
  }
  return j.content ?? '';
}

export async function syncRecipeAST(originalCUE: string, recipeContent: any): Promise<string> {
  const r = await apiPost('/api/v1/recipes/sync-ast', { original_cue: originalCUE, recipe_content: recipeContent });
  if (!r.ok) {
    const j = (await r.json().catch(() => ({}))) as { error?: string };
    throw new Error(j.error || r.statusText);
  }
  const data = await r.json();
  return data.cue;
}

/** Parse a disk recipe (.cue) into a ParsedRecipe via POST /api/v1/recipes/parse. */
export async function parseDiskRecipe(path: string): Promise<ParsedRecipe> {
  const r = await apiPost('/api/v1/recipes/parse', { path });
  const j = (await r.json().catch(() => ({}))) as { recipe?: ParsedRecipe; error?: string };
  if (!r.ok) {
    throw new Error(j.error || `parse failed: ${r.status}`);
  }
  if (!j.recipe) {
    throw new Error('parse: missing recipe in response');
  }
  return j.recipe;
}

export async function fixRecipeErrors(
  recipeContent: Record<string, unknown>,
  errors: any[],
  model: string
): Promise<RecipesAIGraphResponse> {
  const r = await fetch('/api/v1/recipes/assist-fix', {
    method: 'POST',
    headers: { ...apiHeaders(), 'Content-Type': 'application/json' },
    body: JSON.stringify({ recipe_content: recipeContent, errors, model })
  });
  if (!r.ok) {
    const j = (await r.json().catch(() => ({}))) as { error?: string };
    throw new Error(j.error || r.statusText);
  }
  return await r.json();
}

export async function generateRecipe(intent: string, model: string): Promise<RecipesAIGraphResponse> {
  const r = await fetch('/api/v1/recipes/generate', {
    method: 'POST',
    headers: { ...apiHeaders(), 'Content-Type': 'application/json' },
    body: JSON.stringify({ intent, model })
  });
  if (!r.ok) {
    const j = (await r.json().catch(() => ({}))) as { error?: string };
    throw new Error(j.error || r.statusText);
  }
  return await r.json();
}

export async function fetchLibraryRecipes(): Promise<LibraryCategory[]> {
  const r = await apiGet('/api/v1/recipes/library');
  if (!r.ok) {
    const j = (await r.json().catch(() => ({}))) as { error?: string };
    throw new Error(j.error || r.statusText);
  }
  const data = await r.json();
  return data.categories || [];
}

export type StepKindOption = {
  kind: string;
  label: string;
};

const preferredKindOrder = [
  'command', 'script', 'put', 'get', 'template', 'plugin', 'tunnel', 'ai',
  'agent_transfer', 'docker', 'k8s', 'postgres', 'opensearch'
];

const kindLabels: Record<string, string> = {
  ai: 'AI', k8s: 'Kubernetes', postgres: 'Postgres', opensearch: 'OpenSearch', agent_transfer: 'Agent Transfer'
};

function kindSortIndex(kind: string): number {
  const index = preferredKindOrder.indexOf(kind);
  return index === -1 ? preferredKindOrder.length : index;
}

function titleCase(value: string): string {
  return value.split('_').map((part) => part.charAt(0).toUpperCase() + part.slice(1)).join(' ');
}

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

// eslint-disable-next-line @typescript-eslint/no-explicit-any
export function stepSchemaForKind(schema: any, kind?: string): any {
  if (!kind || !schema?.definitions?.[kind]) {
    return schema;
  }
  const def = schema.definitions[kind];
  const defs = def.$defs ?? def.definitions ?? {};
  return resolveRefs(def, defs);
}