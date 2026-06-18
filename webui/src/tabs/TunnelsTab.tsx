import { useCallback, useEffect, useState } from 'react';
import { Alert, Button, Modal, Space, Table, Typography } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { fetchTunnels, fetchTunnelLogs, stopTunnel } from '../api/tunnels';
import type { TunnelInfo } from '../api/types/tunnels';

interface Props {
  onNavigateToSearch: () => void;
}

export function TunnelsTab({ onNavigateToSearch }: Props) {
  const [tunnelsList, setTunnelsList] = useState<TunnelInfo[]>([]);
  const [tunnelsListErr, setTunnelsListErr] = useState<string | null>(null);
  const [tunnelLogOpen, setTunnelLogOpen] = useState<string | null>(null);
  const [tunnelLogContent, setTunnelLogContent] = useState('');
  const [tunnelLogErr, setTunnelLogErr] = useState<string | null>(null);

  const loadTunnels = useCallback(async () => {
    setTunnelsListErr(null);
    try {
      setTunnelsList(await fetchTunnels());
    } catch (e) {
      setTunnelsListErr(e instanceof Error ? e.message : String(e));
    }
  }, []);

  useEffect(() => { void loadTunnels(); }, [loadTunnels]);

  useEffect(() => {
    if (!tunnelLogOpen) { setTunnelLogContent(''); setTunnelLogErr(null); return; }
    fetchTunnelLogs(tunnelLogOpen)
      .then(setTunnelLogContent)
      .catch((e) => setTunnelLogErr(e instanceof Error ? e.message : String(e)));
  }, [tunnelLogOpen]);

  const columns: ColumnsType<TunnelInfo> = [
    { title: 'Host', dataIndex: 'host_name', key: 'host_name' },
    { title: 'Mapping (Local:Remote)', dataIndex: 'mapping', key: 'mapping', render: (v) => <code>{v}</code> },
    { title: 'Status/Started', dataIndex: 'started_at', key: 'started_at' },
    {
      title: 'Actions',
      key: 'actions',
      align: 'right',
      render: (_, t) => (
        <Space>
          <Button size="small" onClick={() => setTunnelLogOpen(t.id)}>Logs</Button>
          <Button size="small" danger onClick={async () => {
            try { await stopTunnel(t.id); await loadTunnels(); }
            catch (e) { setTunnelsListErr(e instanceof Error ? e.message : String(e)); }
          }}>Stop</Button>
        </Space>
      ),
    },
  ];

  return (
    <>
      {tunnelsListErr && <Alert type="error" message={tunnelsListErr} style={{ marginBottom: 12 }} />}
      <Space style={{ marginBottom: 12 }}>
        <Button onClick={() => void loadTunnels()}>Refresh</Button>
      </Space>
      {tunnelsList.length === 0 ? (
        <Typography.Text type="secondary">
          No active tunnels. You can start one from the{' '}
          <Button type="link" style={{ padding: 0 }} onClick={onNavigateToSearch}>Search tab</Button>.
        </Typography.Text>
      ) : (
        <Table dataSource={tunnelsList} columns={columns} rowKey="id" size="small" pagination={false} />
      )}
      <Modal
        open={!!tunnelLogOpen}
        title="Tunnel Logs"
        footer={<Button onClick={() => setTunnelLogOpen(null)}>Close</Button>}
        onCancel={() => setTunnelLogOpen(null)}
        width="min(800px, 94vw)"
        styles={{ body: { maxHeight: '60vh', overflow: 'auto' } }}
      >
        {tunnelLogErr && <Alert type="error" message={tunnelLogErr} style={{ marginBottom: 8 }} />}
        <pre style={{ margin: 0, fontSize: '0.78rem', whiteSpace: 'pre-wrap', wordBreak: 'break-word' }}>
          {tunnelLogContent || 'Loading...'}
        </pre>
      </Modal>
    </>
  );
}
