import type { Node, NodeMouseHandler } from '@xyflow/react';
import { ReactFlow, Background, Controls } from '@xyflow/react';
import '@xyflow/react/dist/style.css';
import type { IDockviewPanelProps } from 'dockview';
import { useWorkspaceStore } from '../store';
import CustomStepNode from '../../CustomStepNode';

const nodeTypes = { step: CustomStepNode };

export function GraphPanel({ params }: IDockviewPanelProps<{ recipeId: string }>) {
  const recipeId = params.recipeId;
  const doc = useWorkspaceStore((s) => s.docs[recipeId]);
  const setSelectedNode = useWorkspaceStore((s) => s.setSelectedNode);

  if (!doc) return <div style={{ padding: 16, color: '#8b949e' }}>No document for {recipeId}</div>;

  const onNodeClick: NodeMouseHandler = (_, node: Node) => setSelectedNode(recipeId, node.id);

  return (
    <div style={{ height: '100%', width: '100%', position: 'relative' }}>
      <ReactFlow
        nodes={doc.nodes}
        edges={doc.edges}
        nodeTypes={nodeTypes}
        onNodeClick={onNodeClick}
        fitView
      >
        <Controls />
        <Background />
      </ReactFlow>
      {doc.rawMode && (
        <div
          style={{
            position: 'absolute',
            inset: 0,
            background: 'rgba(13, 17, 23, 0.85)',
            color: '#8b949e',
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            textAlign: 'center',
            padding: 16,
          }}
        >
          Raw mode — switch to Visual to edit the graph.
        </div>
      )}
    </div>
  );
}
