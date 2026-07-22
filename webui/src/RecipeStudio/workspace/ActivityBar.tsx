import { Button, Tooltip } from 'antd';
import { AppstoreOutlined, CodeOutlined, PlayCircleOutlined } from '@ant-design/icons';
import type { DockviewApi } from 'dockview';

// Only tool panels that are actually registered today (Toolbox, Step editor,
// Run). Records joins this list once its panel lands (Task 11/12).
const TOOLS = [
  { id: 'toolbox', component: 'toolbox', icon: <AppstoreOutlined />, label: 'Toolbox' },
  { id: 'stepeditor', component: 'stepeditor', icon: <CodeOutlined />, label: 'Step editor' },
  { id: 'run', component: 'run', icon: <PlayCircleOutlined />, label: 'Run' },
];

export function ActivityBar({ api }: { api: DockviewApi | null }) {
  const toggle = (id: string, component: string) => {
    if (!api) return;
    const p = api.getPanel(id);
    if (p) p.api.setActive();
    else api.addPanel({ id, component, title: id });
  };
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 8, padding: 8, background: '#001529' }}>
      {TOOLS.map((t) => (
        <Tooltip key={t.id} title={t.label} placement="right">
          <Button type="text" icon={t.icon} onClick={() => toggle(t.id, t.component)} style={{ color: '#e6e6e6' }} />
        </Tooltip>
      ))}
    </div>
  );
}
