import { useEffect, useState } from 'react';
import {
  fetchRecentRuns,
  fetchRecipes,
  type RecentRunEntry,
  type RecipeListEntry,
} from '../api';
import { loadDrafts, deleteDraft } from './drafts';
import type { Draft, RecipeRef } from './types';

type Props = {
  onBack: () => void;
  onPickDisk: (path: string) => void;
  onPickDraft: (draft: Draft) => void;
  onPickRecent: (run: RecentRunEntry) => void;
  onViewSource: (path: string, name: string) => void;
  onAiAssist: (path: string, name: string) => void;
  current: RecipeRef | null;
};

export function StepRecipe(props: Props) {
  const [recent, setRecent] = useState<RecentRunEntry[]>([]);
  const [drafts, setDrafts] = useState<Draft[]>([]);
  const [recipes, setRecipes] = useState<RecipeListEntry[]>([]);
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => {
    setDrafts(loadDrafts());
    (async () => {
      try {
        setRecent(await fetchRecentRuns(10));
        setRecipes(await fetchRecipes());
      } catch (e) {
        setErr(e instanceof Error ? e.message : String(e));
      }
    })();
  }, []);

  return (
    <div className="rcp-step rcp-step--recipe">
      <header className="rcp-step__header">
        <h2>② Pick recipe</h2>
        {err ? <p className="rcp-err">{err}</p> : null}
      </header>

      <section className="rcp-panel">
        <h3>Recent runs</h3>
        {recent.length === 0 ? (
          <p className="rcp-empty">
            No recent cue-exec recordings yet (or session recording is disabled).
          </p>
        ) : (
          <ul className="rcp-list">
            {recent.map((r) => (
              <li key={r.recording_id}>
                <button type="button" className="rcp-link" onClick={() => props.onPickRecent(r)}>
                  {r.recipe_name}
                  {r.edited ? <em> (edited)</em> : null}
                </button>
                <span className="rcp-list__meta">
                  {r.host_count} host{r.host_count === 1 ? '' : 's'} · {timeAgo(r.started_at)}
                </span>
              </li>
            ))}
          </ul>
        )}
      </section>

      <section className="rcp-panel">
        <h3>In-browser drafts</h3>
        {drafts.length === 0 ? (
          <p className="rcp-empty">No drafts saved yet. Edit a recipe on step ③ to save one.</p>
        ) : (
          <ul className="rcp-list">
            {drafts.map((d) => (
              <li key={d.id}>
                <button type="button" className="rcp-link" onClick={() => props.onPickDraft(d)}>
                  {d.name}
                </button>
                <span className="rcp-list__meta">
                  from {basename(d.baseRecipePath)} · {timeAgo(d.modifiedAt)}
                </span>
                <button
                  type="button"
                  className="rcp-btn rcp-btn--ghost"
                  onClick={() => {
                    deleteDraft(d.id);
                    setDrafts(loadDrafts());
                  }}
                >
                  delete
                </button>
              </li>
            ))}
          </ul>
        )}
      </section>

      <section className="rcp-panel">
        <h3>Recipes on disk</h3>
        <ul className="rcp-list">
          {recipes.map((rp) => (
            <li key={rp.path}>
              <button type="button" className="rcp-link" onClick={() => props.onPickDisk(rp.path)}>
                {rp.name}
              </button>
              <span>
                <button
                  type="button"
                  className="rcp-btn rcp-btn--ghost"
                  onClick={() => props.onViewSource(rp.path, rp.name)}
                >
                  view source
                </button>
                <button
                  type="button"
                  className="rcp-btn rcp-btn--ghost"
                  onClick={() => props.onAiAssist(rp.path, rp.name)}
                >
                  AI assist
                </button>
              </span>
            </li>
          ))}
        </ul>
      </section>

      <footer className="rcp-step__footer">
        <button type="button" className="rcp-btn" onClick={props.onBack}>← back</button>
      </footer>
    </div>
  );
}

function timeAgo(iso: string): string {
  const ms = Date.now() - new Date(iso).getTime();
  const m = Math.round(ms / 60000);
  if (m < 60) return `${m}m ago`;
  if (m < 1440) return `${Math.round(m / 60)}h ago`;
  return `${Math.round(m / 1440)}d ago`;
}

function basename(p: string): string {
  return p.split('/').pop() ?? p;
}
