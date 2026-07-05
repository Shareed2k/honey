// webui/src/RecipesTab/drafts.ts
import type { Draft } from './types';
import type { ParsedRecipe } from '../api/types/recipes';

const KEY = 'honey.recipes.drafts.v1';

export function loadDrafts(): Draft[] {
  try {
    const raw = localStorage.getItem(KEY);
    if (!raw) return [];
    const parsed = JSON.parse(raw);
    return Array.isArray(parsed) ? parsed : [];
  } catch {
    return [];
  }
}

export function saveDraft(input: { name: string; baseRecipePath: string; recipe: ParsedRecipe }): Draft {
  const drafts = loadDrafts();
  const draft: Draft = {
    id: cryptoRandomId(),
    name: input.name.trim() || 'untitled',
    baseRecipePath: input.baseRecipePath,
    modifiedAt: new Date().toISOString(),
    recipe: input.recipe,
  };
  drafts.unshift(draft);
  writeDrafts(drafts.slice(0, 50)); // soft cap
  return draft;
}

export function updateDraft(id: string, recipe: ParsedRecipe): Draft | null {
  const drafts = loadDrafts();
  const idx = drafts.findIndex((d) => d.id === id);
  if (idx < 0) return null;
  drafts[idx] = { ...drafts[idx], recipe, modifiedAt: new Date().toISOString() };
  writeDrafts(drafts);
  return drafts[idx];
}

export function deleteDraft(id: string): void {
  writeDrafts(loadDrafts().filter((d) => d.id !== id));
}

function writeDrafts(drafts: Draft[]) {
  try {
    localStorage.setItem(KEY, JSON.stringify(drafts));
  } catch {
    // localStorage quota or disabled — fail silently; in-memory state still works.
  }
}

function cryptoRandomId(): string {
  if (typeof crypto !== 'undefined' && crypto.randomUUID) return crypto.randomUUID();
  const b = new Uint8Array(16);
  crypto.getRandomValues(b);
  return Array.from(b, (x) => x.toString(16).padStart(2, '0')).join('');
}
