import type { IDockviewPanelProps } from 'dockview-react';
import { useWorkspaceStore } from '../store';
import DynamicStepForm from '../../DynamicStepForm';
import { stepSchemaForKind } from '../../../api/recipes';

/**
 * Singleton recipe-defaults/settings editor — like StepEditorPanel/RunPanel/
 * ValidationPanel, it follows the active doc (`s.active`) rather than being
 * opened per-recipe (see registry.ts's DEFAULT_TOOL_PANELS / ActivityBar.tsx).
 * Renders doc.recipeDefaults (the recipe's top-level `defaults` block — see
 * the old useRecipeStudioEngine.ts's recipeDefaults state) through the same
 * DynamicStepForm the step editor uses, scoped to the schema's "defaults"
 * definition via stepSchemaForKind, and writes edits back through
 * store.setRecipeDefaults so they land on the active doc and mark it dirty.
 */
export function SettingsPanel(_props: IDockviewPanelProps) {
  const active = useWorkspaceStore((s) => s.active);
  const schema = useWorkspaceStore((s) => s.schema);
  const doc = useWorkspaceStore((s) => (active ? s.docs[active] : undefined));
  const setRecipeDefaults = useWorkspaceStore((s) => s.setRecipeDefaults);

  if (!doc) {
    return (
      <div style={{ padding: 16, color: '#8b949e' }}>
        No active document — open a recipe to edit its defaults.
      </div>
    );
  }

  return (
    <div style={{ padding: 12, height: '100%', overflowY: 'auto' }}>
      <DynamicStepForm
        schema={stepSchemaForKind(schema, 'defaults')}
        value={doc.recipeDefaults}
        onChange={(v: unknown) => setRecipeDefaults(doc.recipeId, v)}
      />
    </div>
  );
}
