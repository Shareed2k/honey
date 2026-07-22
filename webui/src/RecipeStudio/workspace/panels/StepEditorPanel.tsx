/* eslint-disable @typescript-eslint/no-explicit-any */
import type { IDockviewPanelProps } from 'dockview';
import { useWorkspaceStore } from '../store';
import DynamicStepForm from '../../DynamicStepForm';
import { stepSchemaForKind } from '../../../api/recipes';

export function StepEditorPanel(_props: IDockviewPanelProps) {
  const active = useWorkspaceStore((s) => s.active);
  const schema = useWorkspaceStore((s) => s.schema);
  const doc = useWorkspaceStore((s) => (active ? s.docs[active] : undefined));
  const setStepData = useWorkspaceStore((s) => s.setStepData);

  if (!doc || !doc.selectedNodeId) {
    return <div style={{ padding: 16, color: '#8b949e' }}>Select a step in the graph.</div>;
  }
  const nodeId = doc.selectedNodeId;
  const value = doc.stepData[nodeId];
  return (
    <div style={{ padding: 12 }}>
      <DynamicStepForm
        schema={stepSchemaForKind(schema, value?.kind)}
        value={value}
        onChange={(v: any) => setStepData(doc.recipeId, nodeId, v)}
      />
    </div>
  );
}
