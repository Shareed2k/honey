import { useEffect, useState } from 'react';
import { Alert, Button, Table } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { fetchRecentRuns } from '../api/core';
import { fetchRecipes } from '../api/recipes';
import { type RecentRunEntry, type RecipeListEntry } from '../api/types/recipes';
import { loadDrafts, deleteDraft } from './drafts';
import type { Draft } from './types';
import { useWizard } from './WizardContext';

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
};

export function StepRecipe(props: Props) {
  const { state: { recipe: current } } = useWizard();
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

  const recentColumns: ColumnsType<RecentRunEntry> = [
    {
      title: 'Recipe Name',
      dataIndex: 'recipe_name',
      key: 'recipe_name',
      render: (name: string, r: RecentRunEntry) => (
        <Button type="link" onClick={() => props.onPickRecent(r)}>
          {name}
          {r.edited ? <em> (edited)</em> : null}
        </Button>
      ),
    },
    {
      title: 'Hosts',
      key: 'host_count',
      render: (_: unknown, r: RecentRunEntry) => (
        <span className="rcp-list__meta">{r.host_count} host{r.host_count === 1 ? '' : 's'}</span>
      ),
    },
    {
      title: 'Time',
      key: 'started_at',
      render: (_: unknown, r: RecentRunEntry) => (
        <span className="rcp-list__meta">{timeAgo(r.started_at)}</span>
      ),
    },
    {
      title: 'Actions',
      key: 'actions',
      align: 'right' as const,
      render: (_: unknown, r: RecentRunEntry) => (
        <div className="rcp-table__actions">
          {props.sessionRecordingAvailable && r.recording_id ? (
            <Button size="small" onClick={() => props.onReplay(r)}>
              Replay
            </Button>
          ) : null}
          <Button
            size="small"
            disabled={!r.hosts?.length}
            title={
              r.hosts?.length
                ? 'Load recipe and pre-select hosts from this run'
                : 'No host list saved in this recording'
            }
            onClick={() => void props.onRerunSameHosts(r)}
          >
            Re-run same hosts
          </Button>
        </div>
      ),
    },
  ];

  const draftsColumns: ColumnsType<Draft> = [
    {
      title: 'Name',
      key: 'name',
      render: (_: unknown, d: Draft) => (
        <Button type="link" onClick={() => props.onPickDraft(d)}>
          {d.name}
        </Button>
      ),
    },
    {
      title: 'Base Recipe',
      key: 'baseRecipePath',
      render: (_: unknown, d: Draft) => (
        <span className="rcp-list__meta">{basename(d.baseRecipePath)}</span>
      ),
    },
    {
      title: 'Time',
      key: 'modifiedAt',
      render: (_: unknown, d: Draft) => (
        <span className="rcp-list__meta">{timeAgo(d.modifiedAt)}</span>
      ),
    },
    {
      title: 'Actions',
      key: 'actions',
      align: 'right' as const,
      render: (_: unknown, d: Draft) => (
        <div className="rcp-table__actions">
          <Button
            size="small"
            onClick={() => {
              deleteDraft(d.id);
              setDrafts(loadDrafts());
            }}
          >
            delete
          </Button>
        </div>
      ),
    },
  ];

  const recipesColumns: ColumnsType<RecipeListEntry> = [
    {
      title: 'Name',
      key: 'name',
      render: (_: unknown, rp: RecipeListEntry) => (
        <Button type="link" onClick={() => props.onPickDisk(rp.path)}>
          {rp.name}
        </Button>
      ),
    },
    {
      title: 'Path',
      dataIndex: 'path',
      key: 'path',
      render: (path: string) => <span className="rcp-list__meta">{path}</span>,
    },
    {
      title: 'Actions',
      key: 'actions',
      align: 'right' as const,
      render: (_: unknown, rp: RecipeListEntry) => (
        <div className="rcp-table__actions">
          <Button size="small" onClick={() => props.onViewSource(rp.path, rp.name)}>
            view source
          </Button>
          <Button size="small" onClick={() => props.onAiAssist(rp.path, rp.name)}>
            AI assist
          </Button>
        </div>
      ),
    },
  ];

  return (
    <div className="rcp-step rcp-step--recipe">
      <header className="rcp-step__header">
        <h2>② Pick recipe</h2>
        {err ? <Alert type="error" message={err} style={{ marginTop: 4 }} /> : null}
      </header>

      <section className="rcp-panel">
        <h3>Recent runs</h3>
        {recent.length === 0 ? (
          <p className="rcp-empty">
            No recent cue-exec recordings yet (or session recording is disabled).
          </p>
        ) : (
          <Table
            dataSource={recent}
            columns={recentColumns}
            rowKey="recording_id"
            size="small"
            pagination={false}
          />
        )}
      </section>

      <section className="rcp-panel">
        <h3>In-browser drafts</h3>
        {drafts.length === 0 ? (
          <p className="rcp-empty">No drafts saved yet. Edit a recipe on step ③ to save one.</p>
        ) : (
          <Table
            dataSource={drafts}
            columns={draftsColumns}
            rowKey="id"
            size="small"
            pagination={false}
          />
        )}
      </section>

      <section className="rcp-panel">
        <h3>Recipes on disk</h3>
        {recipes.length === 0 ? (
          <p className="rcp-empty">No recipes found on disk.</p>
        ) : (
          <Table
            dataSource={recipes}
            columns={recipesColumns}
            rowKey="path"
            size="small"
            pagination={false}
          />
        )}
      </section>

      <footer className="rcp-step__footer">
        <Button onClick={props.onBack}>← back</Button>
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
