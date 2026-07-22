import type { DockviewApi } from 'dockview';
import { apiGet, apiPutJson } from '../../api/core';
import { applyDefaultLayout, openRecipeIds } from './registry';

const DEBOUNCE_MS = 800;
const WORKSPACE_PATH = '/api/v1/studio/workspace';

/** Untitled (never-saved) docs have no server-side name — never persist them. */
function isUntitled(id: string): boolean {
  return id.startsWith('untitled-');
}

// The slice of useWorkspaceStore that workspaceSync depends on — kept
// narrow (rather than importing the full store type) so this module stays
// decoupled from the rest of the store's surface.
interface SyncStore {
  createDoc(name: string): Promise<void>;
  setActive(id: string | null): void;
  active: string | null;
}

/**
 * Persists the studio workspace (layout + open recipes + active doc) to the
 * backend and restores it on attach. dockview remains the single source of
 * truth for *what's open*; this only mirrors that state to the server so it
 * survives a reload.
 *
 * - `restore()` runs once, on attach: GET the saved workspace, replay its
 *   layout via `api.fromJSON`, reopen each saved recipe doc, and reselect the
 *   active one. A `restoring` flag is held for the duration so the layout
 *   churn `fromJSON` (and the doc opens that follow it) produce does not
 *   itself trigger a save — without it, restore would immediately PUT back
 *   the layout it just loaded.
 * - Afterwards, every `onDidLayoutChange` schedules a debounced `save()`
 *   (rapid changes — e.g. a drag-resize — collapse into one PUT).
 * - `save()` is fire-and-forget: a failed PUT is swallowed (local dockview/
 *   store state is unaffected either way; the next successful save catches
 *   the workspace up).
 */
export function attachWorkspaceSync(
  api: DockviewApi,
  store: SyncStore,
): { save(): void; dispose(): void } {
  let restoring = false;
  let timer: ReturnType<typeof setTimeout> | null = null;

  const save = () => {
    if (restoring) return;
    const openRecipes = openRecipeIds(api).filter((id) => !isUntitled(id));
    const active = store.active && !isUntitled(store.active) ? store.active : null;
    void apiPutJson(WORKSPACE_PATH, { layout: api.toJSON(), openRecipes, active }).catch(() => {
      // Best-effort persistence: keep dockview/store state as-is and let the
      // next successful save catch the workspace up.
    });
  };

  const scheduleSave = () => {
    if (restoring) return;
    if (timer) clearTimeout(timer);
    timer = setTimeout(save, DEBOUNCE_MS);
  };

  const restore = async () => {
    restoring = true;
    try {
      const res = await apiGet(WORKSPACE_PATH);
      if (!res.ok) return;
      const data = await res.json();
      if (!data || !data.layout) return;

      try {
        api.fromJSON(data.layout);
      } catch {
        // Stale/incompatible layout (e.g. a panel component that no longer
        // exists) — fall through and leave whatever `onReady` already laid
        // out in place.
      }

      const names: string[] = Array.isArray(data.openRecipes) ? data.openRecipes : [];
      for (const name of names.filter((n) => !isUntitled(n))) {
        try {
          await store.createDoc(name);
        } catch {
          // Recipe no longer exists on disk — skip it, don't block the rest.
        }
      }

      if (data.active) store.setActive(data.active);
    } finally {
      restoring = false;
    }
  };

  const disposable = api.onDidLayoutChange(() => scheduleSave());
  void restore();

  return {
    save,
    dispose() {
      if (timer) clearTimeout(timer);
      disposable.dispose();
    },
  };
}

/** Clears the current layout and re-lays the default tool panels. */
export function resetLayout(api: DockviewApi): void {
  api.clear();
  applyDefaultLayout(api);
}
