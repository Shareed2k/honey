import { useEffect, useRef, useState } from 'react';
import { Alert, Button, Card, Modal, Space, Table, Tag, Typography } from 'antd';
import { decideGrant, listGrants, type JitGrantDecision, type JitGrantView } from '../api/jit';

const STATUS_COLORS: Record<string, string> = {
  pending: 'orange',
  approved: 'green',
  denied: 'red',
  revoked: 'default',
};

const REFRESH_INTERVAL_MS = 10_000;

export function AccessRequestsTab() {
  const [grants, setGrants] = useState<JitGrantView[]>([]);
  const [loading, setLoading] = useState(true);
  const [err, setErr] = useState<string | null>(null);
  const [busyId, setBusyId] = useState<string | null>(null);

  // Guards the auto-refresh interval against overlapping requests: a manual
  // Refresh or an in-flight decide should not race a timer tick.
  const busyRef = useRef(false);

  const refresh = () => {
    if (busyRef.current) {
      return;
    }
    setLoading(true);
    listGrants()
      .then(setGrants)
      .catch((e: unknown) => setErr(e instanceof Error ? e.message : String(e)))
      .finally(() => setLoading(false));
  };

  useEffect(refresh, []);

  useEffect(() => {
    const id = setInterval(() => {
      if (busyRef.current) {
        return;
      }
      listGrants()
        .then(setGrants)
        .catch((e: unknown) => setErr(e instanceof Error ? e.message : String(e)));
    }, REFRESH_INTERVAL_MS);
    return () => clearInterval(id);
  }, []);

  const decide = (grant: JitGrantView, decision: JitGrantDecision) => {
    busyRef.current = true;
    setBusyId(grant.id);
    setErr(null);
    decideGrant(grant.id, decision)
      .then(() => {
        listGrants()
          .then(setGrants)
          .catch((e: unknown) => setErr(e instanceof Error ? e.message : String(e)));
      })
      .catch((e: unknown) => setErr(e instanceof Error ? e.message : String(e)))
      .finally(() => {
        busyRef.current = false;
        setBusyId(null);
      });
  };

  const confirmDecide = (grant: JitGrantView, decision: JitGrantDecision) => {
    const verb = decision === 'approve' ? 'Approve' : decision === 'deny' ? 'Deny' : 'Revoke';
    Modal.confirm({
      title: `${verb} access to ${grant.resource.name}?`,
      okText: verb,
      okButtonProps: { danger: decision === 'deny' || decision === 'revoke' },
      onOk: () => decide(grant, decision),
    });
  };

  const columns = [
    {
      title: 'Resource',
      key: 'resource',
      render: (_: unknown, r: JitGrantView) => (
        <span>
          {r.resource.name} <Typography.Text type="secondary">({r.resource.provider})</Typography.Text>
        </span>
      ),
    },
    { title: 'Requester', dataIndex: 'actor', key: 'actor' },
    {
      title: 'Recipient',
      key: 'recipient',
      render: (_: unknown, r: JitGrantView) => r.recipient || '—',
    },
    {
      title: 'Capabilities',
      key: 'capabilities',
      render: (_: unknown, r: JitGrantView) => (
        <Space size={4} wrap>
          {r.capabilities.map((c) => (
            <Tag key={c}>{c}</Tag>
          ))}
        </Space>
      ),
    },
    { title: 'Delivery', dataIndex: 'delivery', key: 'delivery' },
    {
      title: 'Status',
      key: 'status',
      render: (_: unknown, r: JitGrantView) => (
        <Tag color={STATUS_COLORS[r.status] ?? 'default'}>{r.status}</Tag>
      ),
    },
    {
      title: 'Expires',
      key: 'expires_at',
      render: (_: unknown, r: JitGrantView) => (r.expires_at ? new Date(r.expires_at).toLocaleString() : '—'),
    },
    {
      title: 'Uses',
      key: 'uses',
      render: (_: unknown, r: JitGrantView) => `${r.redemptions}/${r.max_redemptions || '∞'}`,
    },
    {
      title: 'Actions',
      key: 'actions',
      render: (_: unknown, r: JitGrantView) => {
        const rowBusy = busyId === r.id;
        if (r.status === 'pending') {
          return (
            <Space size={8}>
              <Button
                size="small"
                type="primary"
                loading={rowBusy}
                disabled={busyId !== null && !rowBusy}
                onClick={() => confirmDecide(r, 'approve')}
              >
                Approve
              </Button>
              <Button
                size="small"
                loading={rowBusy}
                disabled={busyId !== null && !rowBusy}
                onClick={() => confirmDecide(r, 'deny')}
              >
                Deny
              </Button>
            </Space>
          );
        }
        if (r.status === 'approved') {
          return (
            <Button
              size="small"
              danger
              loading={rowBusy}
              disabled={busyId !== null && !rowBusy}
              onClick={() => confirmDecide(r, 'revoke')}
            >
              Revoke
            </Button>
          );
        }
        return <Typography.Text type="secondary">—</Typography.Text>;
      },
    },
  ];

  return (
    <div style={{ width: '100%' }}>
      <Typography.Title level={4}>Access requests</Typography.Title>
      <Typography.Paragraph type="secondary">
        JIT share-link access grants. Requests marked <Tag color="orange">pending</Tag> are awaiting
        approval — approve to activate the link, deny to reject it. Approved grants can be revoked at
        any time.
      </Typography.Paragraph>

      {err && <Alert type="error" message={err} closable onClose={() => setErr(null)} style={{ marginBottom: 12 }} />}

      <Card size="small" title={<Space><span>Grants</span><Button size="small" onClick={refresh}>Refresh</Button></Space>}>
        <Table
          rowKey={(r) => r.id}
          size="small"
          loading={loading}
          columns={columns}
          dataSource={grants}
          pagination={false}
          scroll={{ x: 'max-content' }}
          locale={{ emptyText: 'No access requests yet' }}
        />
      </Card>
    </div>
  );
}
