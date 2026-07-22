import { DockviewReact, type DockviewReadyEvent, type IDockviewPanelProps } from 'dockview';
import 'dockview/dist/styles/dockview.css';
import './workspace/dockview-theme-honey.css';
import { useAppContext } from '../contexts/AppContext';

// Throwaway proof panel (Task 2 only; removed in Task 9 when real panels land).
function HelloPanel(_props: IDockviewPanelProps) {
  const { meta } = useAppContext();
  return <div>context-ok v{meta?.version ?? '?'}</div>;
}

const components = { hello: HelloPanel };

export default function StudioWorkspace() {
  const onReady = (event: DockviewReadyEvent) => {
    event.api.addPanel({ id: 'hello', component: 'hello' });
  };
  return (
    <div style={{ height: '100%', width: '100%' }}>
      <DockviewReact components={components} onReady={onReady} className="dockview-theme-honey" />
    </div>
  );
}
