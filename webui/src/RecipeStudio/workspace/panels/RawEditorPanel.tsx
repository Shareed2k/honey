import { lazy, Suspense } from 'react';
import type { IDockviewPanelProps } from 'dockview-react';
import { useWorkspaceStore } from '../store';

const CodeEditor = lazy(() => import('../../../CodeEditor'));

export function RawEditorPanel({ params }: IDockviewPanelProps<{ recipeId: string }>) {
  const recipeId = params.recipeId;
  const doc = useWorkspaceStore((s) => s.docs[recipeId]);
  const setRawContent = useWorkspaceStore((s) => s.setRawContent);

  if (!doc) return <div style={{ padding: 16, color: '#8b949e' }}>No document for {recipeId}</div>;

  return (
    <div style={{ height: '100%', width: '100%', position: 'relative' }}>
      <Suspense fallback={<div style={{ padding: 16, color: '#8b949e' }}>Loading editor…</div>}>
        <CodeEditor
          value={doc.rawContent}
          onChange={(text: string) => setRawContent(recipeId, text)}
          language="cue"
          height="100%"
        />
      </Suspense>
      {!doc.rawMode && (
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
          Visual mode — switch to Raw to edit CUE.
        </div>
      )}
    </div>
  );
}
