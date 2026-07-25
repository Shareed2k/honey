/* eslint-disable @typescript-eslint/no-explicit-any */
import { Button, Modal, Select } from 'antd';
import { PlusOutlined, ReloadOutlined } from '@ant-design/icons';
import type { IDockviewPanelProps } from 'dockview';
import { useWorkspaceStore } from '../store';
import { listStepKinds } from '../../../api/recipes';
import { recipeStudioSnippets } from '../../useRecipeGraph';

export function ToolboxPanel(_props: IDockviewPanelProps) {
  const active = useWorkspaceStore((s) => s.active);
  const schema = useWorkspaceStore((s) => s.schema);
  const addStep = useWorkspaceStore((s) => s.addStep);
  const addSnippet = useWorkspaceStore((s) => s.addSnippet);
  const resetDoc = useWorkspaceStore((s) => s.resetDoc);

  if (!active) return <div style={{ padding: 16, color: '#8b949e' }}>Open a recipe to add steps.</div>;

  const handleResetCanvas = () => {
    Modal.confirm({
      title: 'Reset canvas',
      content: 'Discard canvas changes?',
      okText: 'Reset',
      okButtonProps: { danger: true },
      onOk: () => resetDoc(active),
    });
  };

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 10, padding: 12, height: '100%', overflowY: 'auto' }}>
      {listStepKinds(schema).map((k: any) => (
        <Button key={k.kind} icon={<PlusOutlined />} onClick={() => addStep(active, k.kind)}>
          {k.label}
        </Button>
      ))}
      <Select
        placeholder="Insert snippet"
        allowClear
        value={undefined}
        options={recipeStudioSnippets.map((s) => ({ value: s.id, label: s.label }))}
        onChange={(snippetId: string) => addSnippet(active, snippetId)}
      />
      <Button icon={<ReloadOutlined />} danger onClick={handleResetCanvas}>
        Reset canvas
      </Button>
    </div>
  );
}
