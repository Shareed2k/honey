import { Button, Space } from 'antd';
import { CodeOutlined } from '@ant-design/icons';
import type { IDockviewPanelProps } from 'dockview';
import { HostPicker, recordKey, type HostRecord } from '../../../HostPicker';
import { useHostSelection } from '../../../contexts/HostSelectionContext';
import { useWorkspaceStore } from '../store';

// Records panel: browse every discovered host record and pick which ones are
// "selected" for the run. Selection MUST live in the shared HostSelection
// context (not local state) — Run (RunPanel/store) reads
// useHostSelection().selectedRecords, so ticking a row here is what actually
// hands hosts to a run. Terminal spawning goes through the store's
// `openTerminal` slot (set by the shell once the Terminal panel exists,
// Task 12) so this panel stays decoupled from the Terminal panel's module.
export function RecordsPanel(_props: IDockviewPanelProps) {
  const { records, selectedKeys, setSelectedKeys } = useHostSelection();
  const openTerminal = useWorkspaceStore((s) => s.openTerminal);

  return (
    <div style={{ padding: 8, height: '100%', overflow: 'auto' }}>
      <HostPicker
        records={records}
        selectedKeys={selectedKeys}
        onToggleRow={(rec) => {
          const key = recordKey(rec);
          // Functional updater (not the render-closure `selectedKeys`): antd
          // Table's rowSelection.onSelectAll calls onToggleRow once per row,
          // synchronously, in a plain loop (see HostPicker.tsx). If each call
          // spread the same stale pre-click `selectedKeys`, only the LAST row
          // toggled would survive React's batching. The functional form
          // queues N updaters that each see the previous one's result, so a
          // synchronous select-all loop toggles every row.
          setSelectedKeys((prev) => ({ ...prev, [key]: !prev[key] }));
        }}
        renderRowActions={(rec: HostRecord) => (
          <Space>
            <Button
              size="small"
              icon={<CodeOutlined />}
              disabled={!openTerminal}
              onClick={() => openTerminal?.(rec)}
            >
              Terminal
            </Button>
          </Space>
        )}
      />
    </div>
  );
}
