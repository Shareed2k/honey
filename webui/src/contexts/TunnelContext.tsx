import { createContext, useContext, useState, type ReactNode } from 'react';
import { Modal, Typography, Input, Button, Space, Alert } from 'antd';
import type { HostRecord } from '../HostPicker';
import { fetchHostPorts } from '../api/exec';
import { startTunnel } from '../api/tunnels';
import { useHostSelection } from './HostSelectionContext';
import { useNavigation } from './NavigationContext';

interface TunnelContextType {
  handleOpenTunnel: (rec: HostRecord) => void;
}

const TunnelContext = createContext<TunnelContextType | null>(null);

export function TunnelProvider({ children }: { children: ReactNode }) {
  const { sshUser } = useHostSelection();
  const { setTab } = useNavigation();

  const [tunnelOpen, setTunnelOpen] = useState<{ record: HostRecord } | null>(null);
  const [tunnelLocalPort, setTunnelLocalPort] = useState('');
  const [tunnelRemotePort, setTunnelRemotePort] = useState('');
  const [tunnelRemoteHost, setTunnelRemoteHost] = useState('');
  const [tunnelBusy, setTunnelBusy] = useState(false);
  const [tunnelErr, setTunnelErr] = useState<string | null>(null);
  const [tunnelPorts, setTunnelPorts] = useState<string[]>([]);
  const [tunnelPortsLoading, setTunnelPortsLoading] = useState(false);
  const [tunnelPortsErr, setTunnelPortsErr] = useState<string | null>(null);

  const handleOpenTunnel = (rec: HostRecord) => {
    setTunnelOpen({ record: rec });
    setTunnelLocalPort('');
    setTunnelRemotePort('');
    setTunnelRemoteHost('');
    setTunnelErr(null);
    setTunnelPorts([]);
    setTunnelPortsErr(null);
    if (rec.provider === 'k8s') {
      setTunnelPortsLoading(false);
      if (rec.meta?.ports) {
        try {
          const parsed = rec.meta.ports.split(',').map((p) => p.trim()).filter(Boolean);
          setTunnelPorts(Array.isArray(parsed) ? parsed : []);
        } catch {
          // ignore
        }
      }
    } else {
      setTunnelPortsLoading(true);
      fetchHostPorts({ ssh_user: sshUser.trim(), record: rec })
        .then((ports) => { setTunnelPorts(ports); })
        .catch((e) => { setTunnelPortsErr(e instanceof Error ? e.message : String(e)); })
        .finally(() => { setTunnelPortsLoading(false); });
    }
  };

  const submitTunnel = async () => {
    if (!tunnelOpen) return;
    setTunnelBusy(true);
    setTunnelErr(null);
    try {
      const lp = tunnelLocalPort.trim();
      const rh = tunnelRemoteHost.trim();
      const rp = tunnelRemotePort.trim();

      let mapping = '';
      if (tunnelOpen.record.provider === 'k8s') {
        mapping = lp && rp ? `${lp}:${rp}` : rp ? rp : '';
      } else {
        mapping = lp && rp ? `${lp}:${rh || 'localhost'}:${rp}` : '';
      }

      if (!mapping) {
        throw new Error('Please specify valid ports.');
      }

      await startTunnel({
        ssh_user: sshUser.trim(),
        record: tunnelOpen.record,
        mapping,
      });
      setTunnelOpen(null);
      setTab('tunnels');
    } catch (e) {
      setTunnelErr(e instanceof Error ? e.message : String(e));
    } finally {
      setTunnelBusy(false);
    }
  };

  return (
    <TunnelContext.Provider value={{ handleOpenTunnel }}>
      {children}
      <Modal maskClosable={false} open={!!tunnelOpen}
        title={tunnelOpen ? `Port Forward / Tunnel — ${tunnelOpen.record.name}` : 'Port Forward / Tunnel'}
        onCancel={() => setTunnelOpen(null)}
        footer={null}
        width="min(420px, 94vw)"
      >
        {tunnelOpen && (
          <div style={{ display: 'flex', flexDirection: 'column', gap: '0.65rem' }}>
            <Typography.Text type="secondary" style={{ fontSize: '0.85rem' }}>
              Configure a tunnel for <strong>{tunnelOpen.record.name}</strong>. The ports will be opened on the machine running the Honey server.
            </Typography.Text>
            <div>
              <Typography.Text style={{ fontSize: '0.85rem' }}>Local port (on server)</Typography.Text>
              <Input style={{ marginTop: 4 }} placeholder="e.g. 8080" value={tunnelLocalPort} onChange={(e) => setTunnelLocalPort(e.target.value)} />
            </div>
            {tunnelOpen.record.provider !== 'k8s' && (
              <div>
                <Typography.Text style={{ fontSize: '0.85rem' }}>Target remote host (optional, defaults to localhost)</Typography.Text>
                <Input style={{ marginTop: 4 }} placeholder="e.g. localhost" value={tunnelRemoteHost} onChange={(e) => setTunnelRemoteHost(e.target.value)} />
              </div>
            )}
            <div>
              <Typography.Text style={{ fontSize: '0.85rem' }}>Target remote port</Typography.Text>
              <Input style={{ marginTop: 4 }} placeholder="e.g. 80" value={tunnelRemotePort} onChange={(e) => setTunnelRemotePort(e.target.value)} />
              {tunnelPortsLoading && <Typography.Text type="secondary" style={{ fontSize: '0.8rem' }}>Detecting open ports…</Typography.Text>}
              {tunnelPortsErr && <Alert type="error" message={`Error detecting ports: ${tunnelPortsErr}`} style={{ marginTop: 4 }} />}
              {tunnelPorts.length > 0 && (
                <Space wrap style={{ marginTop: 4 }}>
                  {tunnelPorts.map((port) => (
                    <Button key={port} size="small" onClick={() => setTunnelRemotePort(port)} style={{ fontFamily: 'monospace' }}>{port}</Button>
                  ))}
                </Space>
              )}
              {!tunnelPortsLoading && !tunnelPortsErr && tunnelPorts.length === 0 && (
                <Typography.Text type="secondary" style={{ fontSize: '0.8rem' }}>No open ports detected.</Typography.Text>
              )}
            </div>
            {tunnelErr && <Alert type="error" message={tunnelErr} />}
            <Button
              type="primary"
              loading={tunnelBusy}
              disabled={!tunnelLocalPort.trim() || !tunnelRemotePort.trim()}
              onClick={() => void submitTunnel()}
            >
              Start Tunnel
            </Button>
          </div>
        )}
      </Modal>
    </TunnelContext.Provider>
  );
}

export function useTunnel() {
  const ctx = useContext(TunnelContext);
  if (!ctx) throw new Error('useTunnel must be used within TunnelProvider');
  return ctx;
}
