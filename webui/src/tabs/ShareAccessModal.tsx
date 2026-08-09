import { useEffect, useState } from 'react';
import { Alert, Button, Checkbox, Input, InputNumber, Modal, Radio, Space, Tag, Typography } from 'antd';
import { QRCodeSVG } from 'qrcode.react';
import { createGrant } from '../api/jit';
import type { CreateGrantResponse, JitCapability, JitDelivery } from '../api/jit';
import type { HostRecord } from '../HostPicker';

export type ShareAccessModalProps = {
  record: HostRecord | null;
  open: boolean;
  onClose: () => void;
};

const CAPABILITY_OPTIONS: { label: string; value: JitCapability }[] = [
  { label: 'Shell', value: 'shell' },
  { label: 'Exec', value: 'exec' },
  { label: 'Tunnel', value: 'tunnel' },
];

const DEFAULT_CAPABILITIES: JitCapability[] = ['shell'];
const DEFAULT_DELIVERY: JitDelivery = 'both';
const DEFAULT_DURATION = '2h';

export function ShareAccessModal({ record, open, onClose }: ShareAccessModalProps) {
  const [duration, setDuration] = useState(DEFAULT_DURATION);
  const [capabilities, setCapabilities] = useState<JitCapability[]>(DEFAULT_CAPABILITIES);
  const [delivery, setDelivery] = useState<JitDelivery>(DEFAULT_DELIVERY);
  const [requireApproval, setRequireApproval] = useState(false);
  const [recipient, setRecipient] = useState('');
  const [reason, setReason] = useState('');
  const [maxRedemptions, setMaxRedemptions] = useState(0);

  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const [result, setResult] = useState<CreateGrantResponse | null>(null);
  const [copied, setCopied] = useState(false);

  // Reset to a blank form every time the modal is opened (fresh target, or the
  // same one re-opened) so a previous grant's result/code never lingers.
  useEffect(() => {
    if (open) {
      setDuration(DEFAULT_DURATION);
      setCapabilities(DEFAULT_CAPABILITIES);
      setDelivery(DEFAULT_DELIVERY);
      setRequireApproval(false);
      setRecipient('');
      setReason('');
      setMaxRedemptions(0);
      setBusy(false);
      setErr(null);
      setResult(null);
      setCopied(false);
    }
  }, [open, record]);

  const onSubmit = async () => {
    if (!record) {
      return;
    }
    setErr(null);
    setBusy(true);
    try {
      const resp = await createGrant({
        resource: {
          name: record.name,
          provider: record.provider,
          primary_ip: record.primary_ip,
          meta: record.meta,
        },
        capabilities,
        delivery,
        duration,
        reason: reason.trim() || undefined,
        require_approval: requireApproval,
        max_redemptions: maxRedemptions,
        recipient: recipient.trim() || undefined,
      });
      setResult(resp);
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  const link = result ? window.location.origin + result.link_path : '';

  const copyLink = () => {
    if (!link) {
      return;
    }
    navigator.clipboard.writeText(link).then(
      () => {
        setCopied(true);
        setTimeout(() => setCopied(false), 1500);
      },
      () => setErr('Could not copy to clipboard'),
    );
  };

  return (
    <Modal
      open={open}
      onCancel={onClose}
      title={record ? `Share access — ${record.name}` : 'Share access'}
      width={480}
      destroyOnHidden
      footer={
        result
          ? [
              <Button key="done" type="primary" onClick={onClose}>
                Done
              </Button>,
            ]
          : [
              <Button key="cancel" onClick={onClose}>
                Cancel
              </Button>,
              <Button
                key="create"
                type="primary"
                loading={busy}
                disabled={!record || capabilities.length === 0}
                onClick={() => void onSubmit()}
              >
                Create link
              </Button>,
            ]
      }
    >
      {err && (
        <Alert type="error" message={err} closable onClose={() => setErr(null)} style={{ marginBottom: 12 }} />
      )}

      {!result ? (
        <Space direction="vertical" size={12} style={{ width: '100%' }}>
          <div>
            <label htmlFor="jit-duration" style={{ display: 'block', marginBottom: 4 }}>
              <Typography.Text>Duration</Typography.Text>
            </label>
            <Input
              id="jit-duration"
              value={duration}
              onChange={(e) => setDuration(e.target.value)}
              placeholder="2h"
              style={{ width: 160 }}
            />
          </div>

          <div>
            <Typography.Text id="jit-capabilities-label" style={{ display: 'block', marginBottom: 4 }}>
              Capabilities
            </Typography.Text>
            <Checkbox.Group
              aria-labelledby="jit-capabilities-label"
              options={CAPABILITY_OPTIONS}
              value={capabilities}
              onChange={(vals) => setCapabilities(vals)}
            />
          </div>

          <div>
            <Typography.Text id="jit-delivery-label" style={{ display: 'block', marginBottom: 4 }}>
              Delivery
            </Typography.Text>
            <Radio.Group
              aria-labelledby="jit-delivery-label"
              value={delivery}
              onChange={(e) => setDelivery(e.target.value as JitDelivery)}
            >
              <Radio value="web">Browser terminal</Radio>
              <Radio value="cert">SSH certificate</Radio>
              <Radio value="both">Both</Radio>
            </Radio.Group>
          </div>

          <div>
            <Typography.Text id="jit-access-label" style={{ display: 'block', marginBottom: 4 }}>
              Access
            </Typography.Text>
            <Radio.Group
              aria-labelledby="jit-access-label"
              value={requireApproval}
              onChange={(e) => setRequireApproval(e.target.value as boolean)}
            >
              <Radio value={false}>Grant now</Radio>
              <Radio value>Require approval</Radio>
            </Radio.Group>
          </div>

          <div>
            <label htmlFor="jit-recipient" style={{ display: 'block', marginBottom: 4 }}>
              <Typography.Text>Recipient (optional)</Typography.Text>
            </label>
            <Input
              id="jit-recipient"
              value={recipient}
              onChange={(e) => setRecipient(e.target.value)}
              placeholder="login / principal"
            />
          </div>

          <div>
            <label htmlFor="jit-reason" style={{ display: 'block', marginBottom: 4 }}>
              <Typography.Text>Reason (optional)</Typography.Text>
            </label>
            <Input
              id="jit-reason"
              value={reason}
              onChange={(e) => setReason(e.target.value)}
              placeholder="Why is this access needed?"
            />
          </div>

          <div>
            <label htmlFor="jit-max-redemptions" style={{ display: 'block', marginBottom: 4 }}>
              <Typography.Text>Max redemptions</Typography.Text>
            </label>
            <InputNumber
              id="jit-max-redemptions"
              min={0}
              value={maxRedemptions}
              onChange={(v) => setMaxRedemptions(v ?? 0)}
              style={{ width: 120 }}
            />
            <Typography.Text type="secondary" style={{ display: 'block', fontSize: 12, marginTop: 2 }}>
              0 = unlimited within the window
            </Typography.Text>
          </div>
        </Space>
      ) : (
        <Space direction="vertical" align="center" size={10} style={{ width: '100%' }}>
          <Tag color={result.status === 'approved' ? 'green' : 'orange'}>
            {result.status === 'approved' ? 'Active link' : 'Awaiting approval'}
          </Tag>
          <div style={{ background: '#fff', padding: 12 }}>
            <QRCodeSVG value={link} size={220} level="M" />
          </div>
          <Space.Compact style={{ width: '100%' }}>
            <Input readOnly value={link} aria-label="Share link" />
            <Button onClick={copyLink}>{copied ? 'Copied' : 'Copy'}</Button>
          </Space.Compact>
          {result.expires_at ? (
            <Typography.Text type="secondary">
              Expires {new Date(result.expires_at).toLocaleString()}
            </Typography.Text>
          ) : null}
          <Typography.Text type="warning">
            Copy this link now — the code is shown only once.
          </Typography.Text>
        </Space>
      )}
    </Modal>
  );
}
