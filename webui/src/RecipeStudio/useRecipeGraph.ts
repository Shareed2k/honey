import { useState, useCallback } from 'react';
import { useNodesState, useEdgesState, addEdge, Connection, Edge } from '@xyflow/react';

export type StepDraft = Record<string, unknown> & {
  id: string;
  kind: string;
  host: string;
};

export type FlowNodeLike = { id: string };
export type FlowEdgeLike = { source: string; target: string };

export type RecipeStudioSnippet = {
  id: string;
  label: string;
  steps: StepDraft[];
  edges: FlowEdgeLike[];
};

export type PositionedNode = {
  id: string;
  position: { x: number; y: number };
  data?: { wave?: number } & Record<string, unknown>;
};

const WAVE_COL_W = 240;
const WAVE_ROW_H = 110;
const WAVE_X0 = 100;
const WAVE_Y0 = 80;

const preferredKindOrder = [
  'command', 'script', 'put', 'get', 'template', 'plugin', 'tunnel', 'ai',
  'agent_transfer', 'docker', 'k8s', 'postgres', 'opensearch',
];

export const recipeStudioSnippets: RecipeStudioSnippet[] = [
  {
    id: 'load_check_ai',
    label: 'Load Check + AI',
    steps: [
      {
        id: 'collect_load',
        kind: 'command',
        host: '*',
        command: 'uptime && free -h && ps -eo pid,ppid,comm,%cpu,%mem --sort=-%cpu | head -20',
      },
      {
        id: 'summarize_load',
        kind: 'ai',
        host: '_',
        ai: {
          prompt: 'Summarize host load anomalies, likely causes, and safe next checks.',
        },
      },
    ],
    edges: [{ source: 'collect_load', target: 'summarize_load' }],
  },
  {
    id: 'k8s_rollout_restart',
    label: 'K8s Rollout Restart',
    steps: [
      {
        id: 'restart_workload',
        kind: 'k8s',
        host: '*',
        k8s: {
          namespace: 'default',
          rollout_restart: { resource: 'deployment/app', wait: true },
        },
      },
      {
        id: 'verify_workload',
        kind: 'k8s',
        host: '*',
        k8s: {
          namespace: 'default',
          get: { resource: 'pods', label_selector: 'app=app', format: 'wide' },
        },
      },
    ],
    edges: [{ source: 'restart_workload', target: 'verify_workload' }],
  },
  {
    id: 'tunnel_postgres_query',
    label: 'Tunnel + Postgres',
    steps: [
      {
        id: 'pg_tunnel',
        kind: 'tunnel',
        host: '*',
        tunnel: { remote_host: '127.0.0.1', remote_port: 5432, local_port: 0 },
      },
      {
        id: 'pg_check',
        kind: 'plugin',
        host: '_',
        plugin: {
          id: 'postgres',
          action: 'query',
          config: {
            dsn_secret: 'PG_DSN',
            tunnel_step: 'pg_tunnel',
            sql: 'select now() as checked_at, version() as version',
          },
        },
      },
    ],
    edges: [{ source: 'pg_tunnel', target: 'pg_check' }],
  },
  {
    id: 'service_restart_verify',
    label: 'Service Restart + Verify',
    steps: [
      {
        id: 'service_before',
        kind: 'command',
        host: '*',
        command: 'systemctl is-active --quiet app.service && echo active || echo inactive',
      },
      {
        id: 'service_restart',
        kind: 'command',
        host: '*',
        run_as: 'root',
        command: 'systemctl restart app.service',
        retry: { attempts: 3, delay_ms: 1000, max_delay_ms: 10000, backoff: 'exponential' },
      },
      {
        id: 'service_verify',
        kind: 'command',
        host: '*',
        command: 'systemctl is-active --quiet app.service && echo ok',
        retry: { attempts: 5, delay_ms: 1000, max_delay_ms: 15000, backoff: 'fixed' },
      },
    ],
    edges: [
      { source: 'service_before', target: 'service_restart' },
      { source: 'service_restart', target: 'service_verify' },
    ],
  },
];

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

function isEmptyObject(value: unknown): boolean {
  return typeof value === 'object' && value !== null && !Array.isArray(value) && Object.keys(value).length === 0;
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

export function collectAncestorNodeIDs(edges: FlowEdgeLike[], targetId: string): Set<string> {
  const out = new Set<string>([targetId]);
  const visit = (id: string) => {
    for (const edge of edges) {
      if (edge.target !== id || out.has(edge.source)) {
        continue;
      }
      out.add(edge.source);
      visit(edge.source);
    }
  };
  visit(targetId);
  return out;
}

export function uniqueStepID(base: string, usedIDs: Set<string>): string {
  const clean = base.trim().replace(/^[^a-zA-Z]+/, '').replace(/[^a-zA-Z0-9_-]/g, '_') || 'step';
  if (!usedIDs.has(clean)) {
    usedIDs.add(clean);
    return clean;
  }

  let suffix = 2;
  while (usedIDs.has(`${clean}_${suffix}`)) {
    suffix++;
  }
  const next = `${clean}_${suffix}`;
  usedIDs.add(next);
  return next;
}

export function recipeNameFromFilename(fileName?: string): string {
  const baseName = (fileName || '').split('/').pop()?.trim() || 'visual-studio-recipe';
  return baseName.replace(/\.cue$/i, '') || 'visual-studio-recipe';
}

// eslint-disable-next-line @typescript-eslint/no-explicit-any
export function buildFlowFromRecipe(recipeJson: any): {
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  nodes: any[];
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  edges: any[];
  stepData: Record<string, StepDraft>;
} {
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const nodes: any[] = [];
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const edges: any[] = [];
  const stepData: Record<string, StepDraft> = {};

  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  (recipeJson.steps || []).forEach((step: any, index: number) => {
    const id = step.id || `step_${index + 1}`;
    const kind = detectStepKind(step);
    nodes.push({
      id,
      type: 'step',
      position: { x: 100 + index * 220, y: 150 },
      data: { label: id, kind, host: step.host || '_' },
    });
    if (step.depends) {
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      step.depends.forEach((depId: any) => {
        edges.push({ id: `edge_from_${depId}_to_${id}`, source: depId, target: id });
      });
    }
    stepData[id] = { ...step, id, kind, host: step.host || '_' };
  });

  return { nodes, edges, stepData };
}

export function computeWavesFromEdges(
  nodes: { id: string }[],
  edges: { source: string; target: string }[],
): Record<string, number> {
  const inDegree: Record<string, number> = {};
  const adj: Record<string, string[]> = {};
  for (const n of nodes) { inDegree[n.id] = 0; adj[n.id] = []; }
  for (const e of edges) {
    adj[e.source].push(e.target);
    inDegree[e.target]++;
  }
  const waveByNode: Record<string, number> = {};
  const queue = nodes.filter((n) => inDegree[n.id] === 0).map((n) => n.id);
  queue.forEach((id) => { waveByNode[id] = 1; });
  while (queue.length > 0) {
    const curr = queue.shift()!;
    for (const next of adj[curr]) {
      waveByNode[next] = Math.max(waveByNode[next] ?? 0, (waveByNode[curr] ?? 1) + 1);
      inDegree[next]--;
      if (inDegree[next] === 0) queue.push(next);
    }
  }
  return waveByNode;
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

// -----------------------------------------------------------------------------
// The Custom Hook
// -----------------------------------------------------------------------------

export function useRecipeGraph() {
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const [nodes, setNodes, onNodesChange] = useNodesState<any>([]);
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const [edges, setEdges, onEdgesChange] = useEdgesState<any>([]);
  const [stepData, setStepData] = useState<Record<string, StepDraft>>({});

  const onConnect = useCallback(
    (params: Connection | Edge) => setEdges((eds) => addEdge(params, eds)),
    [setEdges]
  );

  const resetGraph = useCallback(() => {
    setNodes([]);
    setEdges([]);
    setStepData({});
  }, [setNodes, setEdges, setStepData]);

  return {
    nodes,
    setNodes,
    onNodesChange,
    edges,
    setEdges,
    onEdgesChange,
    onConnect,
    stepData,
    setStepData,
    resetGraph,
  };
}