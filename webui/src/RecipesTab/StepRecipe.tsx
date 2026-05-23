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
  onReplay: (run: RecentRunEntry) => void;
  onRerunSameHosts: (run: RecentRunEntry) => void;
  onViewSource: (path: string, name: string) => void;
  onAiAssist: (path: string, name: string) => void;
  sessionRecordingAvailable: boolean;
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
          <div className="rcp-table-container">
            <table>
              <thead>
                <tr>
                  <th>Recipe Name</th>
                  <th>Hosts</th>
                  <th>Time</th>
                  <th style={{ textAlign: 'right' }}>Actions</th>
                </tr>
              </thead>
              <tbody>
                {recent.map((r) => (
                  <tr key={r.recording_id}>
                    <td>
                      <button type="button" className="rcp-link" onClick={() => props.onPickRecent(r)}>
                        {r.recipe_name}
                        {r.edited ? <em> (edited)</em> : null}
                      </button>
                    </td>
                    <td className="rcp-list__meta">{r.host_count} host{r.host_count === 1 ? '' : 's'}</td>
                    <td className="rcp-list__meta">{timeAgo(r.started_at)}</td>
                    <td>
                      <div className="rcp-table__actions">
                        {props.sessionRecordingAvailable && r.recording_id ? (
                          <button
                            type="button"
                            className="rcp-btn rcp-btn--ghost rcp-btn--small"
                            onClick={() => props.onReplay(r)}
                          >
                            Replay
                          </button>
                        ) : null}
                        <button
                          type="button"
                          className="rcp-btn rcp-btn--ghost rcp-btn--small"
                          disabled={!r.hosts?.length}
                          title={
                            r.hosts?.length
                              ? 'Load recipe and pre-select hosts from this run'
                              : 'No host list saved in this recording'
                          }
                          onClick={() => void props.onRerunSameHosts(r)}
                        >
                          Re-run same hosts
                        </button>
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>

      <section className="rcp-panel">
        <h3>In-browser drafts</h3>
        {drafts.length === 0 ? (
          <p className="rcp-empty">No drafts saved yet. Edit a recipe on step ③ to save one.</p>
        ) : (
          <div className="rcp-table-container">
            <table>
              <thead>
                <tr>
                  <th>Name</th>
                  <th>Base Recipe</th>
                  <th>Time</th>
                  <th style={{ textAlign: 'right' }}>Actions</th>
                </tr>
              </thead>
              <tbody>
                {drafts.map((d) => (
                  <tr key={d.id}>
                    <td>
                      <button type="button" className="rcp-link" onClick={() => props.onPickDraft(d)}>
                        {d.name}
                      </button>
                    </td>
                    <td className="rcp-list__meta">{basename(d.baseRecipePath)}</td>
                    <td className="rcp-list__meta">{timeAgo(d.modifiedAt)}</td>
                    <td>
                      <div className="rcp-table__actions">
                        <button
                          type="button"
                          className="rcp-btn rcp-btn--ghost rcp-btn--small"
                          onClick={() => {
                            deleteDraft(d.id);
                            setDrafts(loadDrafts());
                          }}
                        >
                          delete
                        </button>
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>

      <section className="rcp-panel">
        <h3>Recipes on disk</h3>
        {recipes.length === 0 ? (
          <p className="rcp-empty">No recipes found on disk.</p>
        ) : (
          <div className="rcp-table-container">
            <table>
              <thead>
                <tr>
                  <th>Name</th>
                  <th>Path</th>
                  <th style={{ textAlign: 'right' }}>Actions</th>
                </tr>
              </thead>
              <tbody>
                {recipes.map((rp) => (
                  <tr key={rp.path}>
                    <td>
                      <button type="button" className="rcp-link" onClick={() => props.onPickDisk(rp.path)}>
                        {rp.name}
                      </button>
                    </td>
                    <td className="rcp-list__meta">{rp.path}</td>
                    <td>
                      <div className="rcp-table__actions">
                        <button
                          type="button"
                          className="rcp-btn rcp-btn--ghost rcp-btn--small"
                          onClick={() => props.onViewSource(rp.path, rp.name)}
                        >
                          view source
                        </button>
                        <button
                          type="button"
                          className="rcp-btn rcp-btn--ghost rcp-btn--small"
                          onClick={() => props.onAiAssist(rp.path, rp.name)}
                        >
                          AI assist
                        </button>
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
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
