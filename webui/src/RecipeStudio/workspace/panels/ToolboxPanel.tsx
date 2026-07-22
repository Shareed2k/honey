/* eslint-disable @typescript-eslint/no-explicit-any */
import { Button } from 'antd';
import { PlusOutlined } from '@ant-design/icons';
import type { IDockviewPanelProps } from 'dockview';
import { useWorkspaceStore } from '../store';
import { listStepKinds } from '../../../api/recipes';

export function ToolboxPanel(_props: IDockviewPanelProps) {
  const active = useWorkspaceStore((s) => s.active);
  const schema = useWorkspaceStore((s) => s.schema);
  const addStep = useWorkspaceStore((s) => s.addStep);

  if (!active) return <div style={{ padding: 16, color: '#8b949e' }}>Open a recipe to add steps.</div>;

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 10, padding: 12 }}>
      {listStepKinds(schema).map((k: any) => (
        <Button key={k.kind} icon={<PlusOutlined />} onClick={() => addStep(active, k.kind)}>
          {k.label}
        </Button>
      ))}
    </div>
  );
}
