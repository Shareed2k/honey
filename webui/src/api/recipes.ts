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

export async function syncRecipeAST(originalCUE: string, recipeContent: Record<string, unknown>): Promise<string> {
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

/** Response shape of GET /api/v1/recipes/store/{name} (see internal/webserver/recipe_studio_handlers.go StoreLoadResponse). */
export type StoreLoadResponse = {
  recipe: ParsedRecipe;
  raw_cue: string;
  plan?: string;
  steps?: ResolvedStep[];
  graph?: RecipeGraphPlan;
  errors?: ValidationError[];
};

/**
 * Load a STORED recipe by name via GET /api/v1/recipes/store/{name} — resolved against the
 * configured recipe store and looked up by name, with no path validation. Unlike
 * parseDiskRecipe's POST /api/v1/recipes/parse (which requires an absolute path present in
 * the server's allow-list and rejects bare filenames with "recipe path not allowed"), this
 * is the correct way for the Studio to open an already-saved recipe.
 */
export async function fetchStoredRecipe(name: string): Promise<StoreLoadResponse> {
  const r = await apiGet(`/api/v1/recipes/store/${encodeURIComponent(name)}`);
  if (!r.ok) {
    throw new Error(await r.text());
  }
  return r.json();
}

/**
 * Lists the recipe store's contents via GET /api/v1/recipes/store (handleRecipesStoreList) —
 * a bare `{name, path}[]` array (unlike fetchRecipes' GET /api/v1/recipes, which lists
 * disk-discoverable allow-listed paths, a different listing entirely). Used to compute a
 * collision-free store filename before importing an external recipe (Library/Git-load) —
 * see uniqueStoreName in workspace/store.ts.
 */
export async function fetchRecipeStoreList(): Promise<RecipeListEntry[]> {
  const r = await apiGet('/api/v1/recipes/store');
  if (!r.ok) {
    const j = (await r.json().catch(() => ({}))) as { error?: string };
    throw new Error(j.error || r.statusText);
  }
  const data = await r.json();
  return Array.isArray(data) ? data : [];
}

/**
 * Saves CUE content into the recipe store under `name` (must end in `.cue`) via
 * POST /api/v1/recipes/store/{name} (handleRecipesStoreSave) — writes the CUE as-is, no
 * parsing. Used by the Library/Git-load "import into the store" flows: content lands in the
 * store first, then a subsequent GET /api/v1/recipes/store/{name} (fetchStoredRecipe) converts
 * it to recipe JSON the same way opening any other stored recipe does.
 */
export async function saveStoredRecipe(name: string, content: string): Promise<void> {
  const r = await apiPost(`/api/v1/recipes/store/${encodeURIComponent(name)}`, { content });
  if (!r.ok) {
    throw new Error(await r.text());
  }
}

export async function fixRecipeErrors(
  recipeContent: Record<string, unknown>,
  errors: unknown[],
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
  'command', 'script', 'put', 'get', 'template', 'plugin', 'tunnel', 'ai', 'summarize',
  'agent_transfer', 'docker', 'k8s', 'postgres', 'opensearch'
];

const kindLabels: Record<string, string> = {
  ai: 'AI', summarize: 'Summarize', k8s: 'Kubernetes', postgres: 'Postgres', opensearch: 'OpenSearch', agent_transfer: 'Agent Transfer'
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