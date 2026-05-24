import { memo, useCallback, useMemo, useState } from 'react';
import {
  Background,
  Controls,
  Handle,
  Position,
  ReactFlow,
  type Edge,
  type Node,
  type NodeProps,
} from '@xyflow/react';
import '@xyflow/react/dist/style.css';
import type { GraphPlanNode, RecipeGraphPlan } from '../api';

const COL_W = 240;
const ROW_H = 110;

type StepNodeData = {
  node: GraphPlanNode;
};

function RecipeStepNode({ data }: NodeProps<Node<StepNodeData>>) {
  const n = data.node;
  return (
    <div className={'rcp-graph-node' + (n.kv_tunnel ? ' rcp-graph-node--kv' : '') + (n.kind === 'template' ? ' rcp-graph-node--template' : '')}>
      <Handle type="target" position={Position.Left} className="rcp-graph-handle" />
      <div className="rcp-graph-node__id">{n.id}</div>
      <div className="rcp-graph-node__kind">{n.kind}</div>
      {n.wave ? <span className="rcp-graph-node__wave">wave {n.wave}</span> : null}
      {n.retry || n.notify ? (
        <div className="rcp-graph-node__badges">
          {n.retry ? <span className="rcp-graph-node__badge">retry</span> : null}
          {n.notify ? <span className="rcp-graph-node__badge rcp-graph-node__badge--notify">notify</span> : null}
        </div>
      ) : null}
      <Handle type="source" position={Position.Right} className="rcp-graph-handle" />
    </div>
  );
}

const nodeTypes = { recipeStep: memo(RecipeStepNode) };

function buildFlow(plan: RecipeGraphPlan): { nodes: Node<StepNodeData>[]; edges: Edge[] } {
  const waves = plan.waves?.length ? plan.waves : [plan.nodes];
  const nodes: Node<StepNodeData>[] = [];
  waves.forEach((wave, wi) => {
    wave.forEach((n, ri) => {
      nodes.push({
        id: n.id,
        type: 'recipeStep',
        position: { x: wi * COL_W, y: ri * ROW_H },
        data: { node: n },
      });
    });
  });
  const edges: Edge[] = plan.edges.map((e) => ({
    id: `${e.from}->${e.to}`,
    source: e.from,
    target: e.to,
    animated: true,
  }));
  return { nodes, edges };
}

type Props = {
  plan: RecipeGraphPlan;
};

export function RecipeGraphFlow({ plan }: Props) {
  const [selected, setSelected] = useState<GraphPlanNode | null>(null);
  const { nodes, edges } = useMemo(() => buildFlow(plan), [plan]);

  const onNodeClick = useCallback((_: React.MouseEvent, node: Node<StepNodeData>) => {
    setSelected(node.data.node);
  }, []);

  return (
    <div className="rcp-graph">
      <div className="rcp-graph__canvas">
        <ReactFlow
          nodes={nodes}
          edges={edges}
          nodeTypes={nodeTypes}
          nodesDraggable={false}
          nodesConnectable={false}
          elementsSelectable
          fitView
          onNodeClick={onNodeClick}
          proOptions={{ hideAttribution: true }}
        >
          <Background gap={16} color="#30363d" />
          <Controls />
        </ReactFlow>
      </div>
      {selected ? (
        <aside className="rcp-graph__detail">
          <strong>{selected.id}</strong>
          <p>
            kind: {selected.kind}
            <br />
            host: {selected.host}
            {selected.wave ? (
              <>
                <br />
                wave: {selected.wave}
              </>
            ) : null}
            {selected.when ? (
              <>
                <br />
                when: {selected.when}
              </>
            ) : null}
            {selected.retry ? (
              <>
                <br />
                retry: {selected.retry}
              </>
            ) : null}
            {selected.notify ? (
              <>
                <br />
                notify: yes
              </>
            ) : null}
            {selected.kv_tunnel ? (
              <>
                <br />
                kv_tunnel: yes
              </>
            ) : null}
          </p>
          {selected.preview ? <pre>{selected.preview}</pre> : null}
        </aside>
      ) : (
        <p className="rcp-graph__hint">Click a step to see its preview.</p>
      )}
    </div>
  );
}
