import type { DockviewApi } from 'dockview';

/**
 * Parses a dockview panel id produced by `openGraph`/`openRaw` back into the
 * recipeId it was opened for. Returns `null` for panel ids that don't follow
 * the `graph:<id>` / `raw:<id>` convention (e.g. the static tool panels).
 */
export function recipeIdFromPanelId(panelId: string): string | null {
  const m = /^(?:graph|raw):(.+)$/.exec(panelId);
  return m ? m[1] : null;
}

/**
 * Opens (or focuses) the graph panel for a recipe. If a panel with id
 * `graph:<recipeId>` already exists it is brought to front via `setActive`
 * instead of being re-added.
 */
export function openGraph(api: DockviewApi, recipeId: string): void {
  const id = `graph:${recipeId}`;
  const existing = api.getPanel(id);
  if (existing) {
    existing.api.setActive();
    return;
  }
  api.addPanel({ id, component: 'graph', params: { recipeId }, title: recipeId });
}

/**
 * Opens (or focuses) the raw-editor panel for a recipe, placed as a tab
 * beside the corresponding graph panel.
 */
export function openRaw(api: DockviewApi, recipeId: string): void {
  const id = `raw:${recipeId}`;
  const existing = api.getPanel(id);
  if (existing) {
    existing.api.setActive();
    return;
  }
  api.addPanel({
    id,
    component: 'raweditor',
    params: { recipeId },
    title: `${recipeId} (raw)`,
    position: { referencePanel: `graph:${recipeId}` },
  });
}

/**
 * First-run panel arrangement, consumed by the shell (Task 9) to build the
 * default layout. Documented intended positions (applied by the shell when
 * it lays out groups, not encoded as dockview `position` options here since
 * this descriptor precedes the panels it would reference):
 *  - `toolbox`: left-most column.
 *  - `records`: beside `toolbox`, to its right.
 *  - `stepeditor`: right-hand column, above `run`.
 *  - `run`: right-hand column, below `stepeditor`.
 */
export const DEFAULT_TOOL_PANELS = [
  { id: 'toolbox', component: 'toolbox', title: 'Toolbox' },
  { id: 'records', component: 'records', title: 'Records' },
  { id: 'stepeditor', component: 'stepeditor', title: 'Step' },
  { id: 'run', component: 'run', title: 'Run' },
] as const;

/**
 * Returns the unique recipeIds currently open across `graph:`/`raw:` panels
 * (deduplicated — a recipe with both a graph and a raw panel open counts
 * once). Used by workspaceSync to build the `openRecipes` list it persists.
 */
export function openRecipeIds(api: DockviewApi): string[] {
  const ids = new Set<string>();
  for (const panel of api.panels) {
    const id = recipeIdFromPanelId(panel.id);
    if (id) ids.add(id);
  }
  return [...ids];
}

/**
 * Applies the first-run tool panel arrangement (Task 9's shell layout):
 * toolbox on the left (Records tabbed alongside it), step editor to the
 * right, run panel docked below the step editor. Shared by the shell's
 * `onReady` (initial layout, before a saved workspace — if any — is
 * restored over it) and `resetLayout` (Task 15's "Reset Layout" action).
 */
export function applyDefaultLayout(api: DockviewApi): void {
  api.addPanel({ id: 'toolbox', component: 'toolbox', title: 'Toolbox' });
  api.addPanel({
    id: 'records',
    component: 'records',
    title: 'Records',
    position: { referencePanel: 'toolbox', direction: 'within' },
  });
  api.addPanel({
    id: 'stepeditor',
    component: 'stepeditor',
    title: 'Step',
    position: { direction: 'right' },
  });
  api.addPanel({ id: 'run', component: 'run', title: 'Run', position: { direction: 'below' } });
}
