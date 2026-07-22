import { useState } from 'react';
import type { IDockviewHeaderActionsProps } from 'dockview';
import { Button, Space, message } from 'antd';
import { CodeOutlined, EyeOutlined, CheckCircleOutlined, SaveOutlined } from '@ant-design/icons';
import { useWorkspaceStore } from './store';
import { recipeIdFromPanelId } from './registry';
import StorageModal from '../StorageModal';

/**
 * dockview `rightHeaderActionsComponent` — rendered in EVERY group's header.
 * Recipe-scoped actions (Raw⇄Visual / Validate / Save) only make sense for a
 * recipe editor panel (`graph:<id>` / `raw:<id>`), so this returns null for
 * every other group (toolbox/step/run/etc.), which then shows no buttons.
 */
export function EditorHeaderActions(props: IDockviewHeaderActionsProps) {
  const [saveVisible, setSaveVisible] = useState(false);
  const recipeId = props.activePanel ? recipeIdFromPanelId(props.activePanel.id) : null;
  const doc = useWorkspaceStore((s) => (recipeId ? s.docs[recipeId] : undefined));
  const switchToRaw = useWorkspaceStore((s) => s.switchToRaw);
  const switchToVisual = useWorkspaceStore((s) => s.switchToVisual);
  const validate = useWorkspaceStore((s) => s.validate);
  const save = useWorkspaceStore((s) => s.save);

  if (!recipeId || !doc) return null;

  const handleValidate = async () => {
    await validate(recipeId);
    const updated = useWorkspaceStore.getState().docs[recipeId];
    if (!updated) return;
    if (updated.validation.state === 'valid') {
      message.success('Recipe is valid');
    } else {
      message.error(`${updated.validation.issues.length} validation issue(s)`);
    }
  };

  const handleSave = async (options: {
    storage: string;
    path: string;
    commitMessage: string;
    gitUrl?: string;
    gitBranch?: string;
  }) => {
    try {
      await save(recipeId, options);
      setSaveVisible(false);
      message.success('Saved');
    } catch (err) {
      message.error(err instanceof Error ? err.message : String(err));
      throw err;
    }
  };

  return (
    <Space size={4} style={{ marginRight: 8 }}>
      {doc.rawMode ? (
        <Button size="small" icon={<EyeOutlined />} onClick={() => switchToVisual(recipeId)}>
          Visual
        </Button>
      ) : (
        <Button size="small" icon={<CodeOutlined />} onClick={() => switchToRaw(recipeId)}>
          Raw
        </Button>
      )}
      <Button size="small" icon={<CheckCircleOutlined />} onClick={handleValidate}>
        Validate
      </Button>
      <Button size="small" icon={<SaveOutlined />} onClick={() => setSaveVisible(true)}>
        Save
      </Button>
      <StorageModal
        visible={saveVisible}
        currentRecipeName={doc.recipeId}
        onCancel={() => setSaveVisible(false)}
        onSave={handleSave}
      />
    </Space>
  );
}
