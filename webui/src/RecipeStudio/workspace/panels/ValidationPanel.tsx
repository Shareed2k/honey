import { useState } from 'react';
import type { IDockviewPanelProps } from 'dockview-react';
import { Alert, Button, message } from 'antd';
import { CheckCircleOutlined } from '@ant-design/icons';
import { useWorkspaceStore } from '../store';
import type { RiskReport } from '../../../api/types/core';

/**
 * Singleton validation-results panel — like RunPanel, it follows the active
 * doc (`s.active`) rather than being opened per-recipe (see registry.ts's
 * DEFAULT_TOOL_PANELS / ActivityBar.tsx). Displays the last `validate()`
 * run's state/issues/risk for the active doc, and offers "Fix with AI"
 * (store.fixWithAI, ported from the old useRecipeStudioEngine's
 * handleFixWithAI) whenever there are issues to fix.
 */
export function ValidationPanel(_props: IDockviewPanelProps) {
  const active = useWorkspaceStore((s) => s.active);
  const doc = useWorkspaceStore((s) => (active ? s.docs[active] : undefined));
  const fixWithAI = useWorkspaceStore((s) => s.fixWithAI);
  const [fixBusy, setFixBusy] = useState(false);

  if (!doc) {
    return (
      <div style={{ padding: 16, color: '#8b949e' }}>
        No active document — open a recipe to see validation results.
      </div>
    );
  }

  const { validation } = doc;
  const risk = validation.risk as RiskReport | undefined;
  // Cap the rendered list — a recipe with dozens of issues would otherwise
  // blow out the panel; mirrors the old engine's validation strip.
  const shownIssues = validation.issues.slice(0, 20);

  const handleFix = async () => {
    setFixBusy(true);
    try {
      await fixWithAI(active as string);
      message.success('AI fix applied');
    } catch (err) {
      message.error('AI fix failed: ' + (err instanceof Error ? err.message : String(err)));
    } finally {
      setFixBusy(false);
    }
  };

  return (
    <div style={{ padding: 12, height: '100%', overflowY: 'auto' }}>
      {validation.state === 'validating' && <Alert type="info" showIcon message="Validating…" />}
      {validation.state === 'valid' && <Alert type="success" showIcon message="Recipe is valid" />}
      {validation.state === 'idle' && validation.issues.length === 0 && (
        <div style={{ color: '#8b949e' }}>Run Validate to check this recipe.</div>
      )}

      {validation.issues.length > 0 && (
        <Alert
          type="error"
          showIcon
          style={{ marginTop: 8 }}
          message={`${validation.issues.length} issue${validation.issues.length === 1 ? '' : 's'}`}
          description={
            <ul style={{ margin: 0, paddingLeft: 20 }}>
              {shownIssues.map((issue, i) => (
                <li key={i}>
                  {issue.path ? `${issue.path}: ` : ''}
                  {issue.message}
                </li>
              ))}
            </ul>
          }
        />
      )}

      {risk && risk.score > 0 && (
        <Alert
          style={{ marginTop: 8 }}
          type={risk.level === 'High' ? 'error' : risk.level === 'Medium' ? 'warning' : 'info'}
          showIcon
          message={`Risk Level: ${risk.level} (Score: ${risk.score})`}
          description={
            <ul style={{ margin: 0, paddingLeft: 20 }}>
              {risk.findings.map((f, i) => (
                <li key={i}>{f}</li>
              ))}
            </ul>
          }
        />
      )}

      {validation.issues.length > 0 && (
        <Button
          type="primary"
          icon={<CheckCircleOutlined />}
          loading={fixBusy}
          style={{ marginTop: 12 }}
          onClick={handleFix}
        >
          Fix with AI
        </Button>
      )}
    </div>
  );
}
