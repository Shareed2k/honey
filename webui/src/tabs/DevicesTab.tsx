import { useEffect, useState } from 'react';
import { Button, Card, Input, Space, Table, Typography, Alert, Modal } from 'antd';
import { QRCodeSVG } from 'qrcode.react';
import {
  mintEnrollCode, listDevices, bootstrapFor,
  type DeviceRecord, type MintEnrollCodeResponse,
} from '../api/devices';

export function DevicesTab() {
  const [cn, setCn] = useState('');
  const [enrollBase, setEnrollBase] = useState('');
  const [mint, setMint] = useState<MintEnrollCodeResponse | null>(null);
  const [devices, setDevices] = useState<DeviceRecord[]>([]);
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const refresh = () => {
    listDevices().then(setDevices).catch((e) => setErr(String(e.message ?? e)));
  };
  useEffect(refresh, []);

  const onEnroll = async () => {
    setErr(null);
    setBusy(true);
    try {
      setMint(await mintEnrollCode(cn.trim() || undefined));
    } catch (e) {
      setErr(String((e as Error).message ?? e));
    } finally {
      setBusy(false);
    }
  };

  const boot = mint ? bootstrapFor(mint, enrollBase) : null;
  const qrValue = boot ? JSON.stringify(boot) : '';

  const columns = [
    { title: 'Device (CN)', dataIndex: 'cn', key: 'cn' },
    { title: 'Cert fingerprint', dataIndex: 'fingerprint', key: 'fingerprint', ellipsis: true },
    { title: 'Issued', dataIndex: 'issued_at', key: 'issued_at', render: (v: string) => new Date(v).toLocaleString() },
    { title: 'Expires', dataIndex: 'not_after', key: 'not_after', render: (v: string) => new Date(v).toLocaleString() },
  ];

  return (
    <div style={{ maxWidth: 900 }}>
      <Typography.Title level={4}>Device enrollment (mTLS)</Typography.Title>
      <Typography.Paragraph type="secondary">
        Mint a one-time code and scan the QR from the honey app to issue it a client
        certificate. The gateway (APISIX) verifies the cert and maps it to the device
        identity — see <code>examples/mtls/apisix</code>.
      </Typography.Paragraph>

      {err && <Alert type="error" message={err} closable onClose={() => setErr(null)} style={{ marginBottom: 12 }} />}

      <Card size="small" style={{ marginBottom: 16 }}>
        <Space wrap>
          <Input
            placeholder="Device CN (optional, e.g. device:phone-1)"
            value={cn}
            onChange={(e) => setCn(e.target.value)}
            style={{ width: 260 }}
          />
          <Input
            placeholder="Enroll base URL (optional; default this origin)"
            value={enrollBase}
            onChange={(e) => setEnrollBase(e.target.value)}
            style={{ width: 320 }}
          />
          <Button type="primary" loading={busy} onClick={onEnroll}>Enroll device</Button>
        </Space>
      </Card>

      <Modal
        open={!!mint}
        onCancel={() => { setMint(null); refresh(); }}
        onOk={() => { setMint(null); refresh(); }}
        title="Scan to enroll"
        width={420}
      >
        {boot && (
          <Space direction="vertical" align="center" style={{ width: '100%' }}>
            <div style={{ background: '#fff', padding: 12 }}>
              <QRCodeSVG value={qrValue} size={256} level="M" />
            </div>
            <Typography.Text>CN: <code>{boot.cn}</code></Typography.Text>
            <Typography.Text type="secondary" style={{ fontSize: 12 }}>
              enroll URL: {boot.enroll_url}
            </Typography.Text>
            <Typography.Text type="secondary" style={{ fontSize: 12, wordBreak: 'break-all' }}>
              CA fingerprint: {boot.ca_fingerprint}
            </Typography.Text>
            <Typography.Text type="warning" style={{ fontSize: 12 }}>
              One-time code, expires in {mint?.expires_in}s.
            </Typography.Text>
          </Space>
        )}
      </Modal>

      <Card size="small" title={<Space><span>Enrolled devices</span><Button size="small" onClick={refresh}>Refresh</Button></Space>}>
        <Table
          rowKey={(r) => r.fingerprint}
          size="small"
          columns={columns}
          dataSource={devices}
          pagination={false}
          locale={{ emptyText: 'No devices enrolled yet' }}
        />
      </Card>
    </div>
  );
}
