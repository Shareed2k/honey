/* eslint-disable @typescript-eslint/no-explicit-any */
import type { IDockviewPanelProps } from 'dockview';
import { Button, message } from 'antd';
import { PlayCircleOutlined } from '@ant-design/icons';
import { useWorkspaceStore } from '../store';
import DynamicStepForm from '../../DynamicStepForm';
import { stepSchemaForKind } from '../../../api/recipes';
import { useHostSelection } from '../../../contexts/HostSelectionContext';
import { useRunTrigger, type RunMode } from '../useRunTrigger';

export function StepEditorPanel(_props: IDockviewPanelProps) {
  const active = useWorkspaceStore((s) => s.active);
  const schema = useWorkspaceStore((s) => s.schema);
  const doc = useWorkspaceStore((s) => (active ? s.docs[active] : undefined));
  const setStepData = useWorkspaceStore((s) => s.setStepData);
  const { selectedRecords } = useHostSelection();
  const { run, promptModal } = useRunTrigger(active);

  if (!doc || !doc.selectedNodeId) {
    return <div style={{ padding: 16, color: '#8b949e' }}>Select a step in the graph.</div>;
  }
  const nodeId = doc.selectedNodeId;
  const value = doc.stepData[nodeId];

  const triggerRun = (mode: RunMode) => {
    if (selectedRecords.length === 0) {
      message.warning('Select hosts in the Records panel first');
      return;
    }
    run(nodeId, mode);
  };

  return (
    <div style={{ padding: 12 }}>
      <DynamicStepForm
        schema={stepSchemaForKind(schema, value?.kind)}
        value={value}
        onChange={(v: any) => setStepData(doc.recipeId, nodeId, v)}
      />
      <Button
        type="primary"
        icon={<PlayCircleOutlined />}
        style={{ marginTop: 16, width: '100%' }}
        onClick={() => triggerRun('upstream')}
      >
        Run Step
      </Button>
      <Button
        icon={<PlayCircleOutlined />}
        style={{ marginTop: 8, width: '100%' }}
        onClick={() => triggerRun('downstream')}
      >
        Resume from here
      </Button>
      {promptModal}
    </div>
  );
}
