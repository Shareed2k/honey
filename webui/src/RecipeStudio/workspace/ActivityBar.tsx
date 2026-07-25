import { Button, Tooltip } from 'antd';
import { AppstoreOutlined, CodeOutlined, PlayCircleOutlined, DatabaseOutlined, AlertOutlined, SettingOutlined } from '@ant-design/icons';
import type { DockviewApi } from 'dockview';

// Only tool panels that are actually registered today (Toolbox, Step editor,
// Settings, Run, Records, Validation). Terminal joins this list once its
// panel lands (Task 12).
const TOOLS = [
  { id: 'toolbox', component: 'toolbox', icon: <AppstoreOutlined />, label: 'Toolbox' },
  { id: 'records', component: 'records', icon: <DatabaseOutlined />, label: 'Records' },
  { id: 'stepeditor', component: 'stepeditor', icon: <CodeOutlined />, label: 'Step editor' },
  { id: 'settings', component: 'settings', icon: <SettingOutlined />, label: 'Settings' },
  { id: 'run', component: 'run', icon: <PlayCircleOutlined />, label: 'Run' },
  { id: 'validation', component: 'validation', icon: <AlertOutlined />, label: 'Validation' },
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
