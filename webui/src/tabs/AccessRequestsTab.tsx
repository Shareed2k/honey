import { useEffect, useRef, useState } from 'react';
import { Alert, Button, Card, Modal, Space, Table, Tag, Tooltip, Typography } from 'antd';
import {
  decideGrant,
  deleteGrant,
  killShareSession,
  listGrants,
  listShareSessions,
  purgeGrants,
  type JitGrantDecision,
  type JitGrantView,
  type ShareSessionView,
} from '../api/jit';
import { ShareWatchModal } from './ShareWatchModal';

const STATUS_COLORS: Record<string, string> = {
  pending: 'orange',
  approved: 'green',
  denied: 'red',
  revoked: 'default',
};

const REFRESH_INTERVAL_MS = 10_000;
const DEFAULT_PAGE_SIZE = 10;

/**
 * Mirrors the store's terminal predicate closely enough for a UI enable/
 * disable decision: denied/revoked are always terminal, and an approved
 * grant is terminal once past its expiry. The server is the actual authority
 * (a delete it disagrees with comes back 409), this only avoids offering a
 * Delete button that will predictably fail.
 */
function isTerminalGrant(g: JitGrantView): boolean {
  if (g.status === 'denied' || g.status === 'revoked') {
    return true;
  }
  if (g.status === 'approved' && g.expires_at) {
    return new Date(g.expires_at).getTime() <= Date.now();
  }
  return false;
}

export function AccessRequestsTab() {
  const [grants, setGrants] = useState<JitGrantView[]>([]);
  const [grantsTotal, setGrantsTotal] = useState(0);
  const [grantsPage, setGrantsPage] = useState(1);
  const [grantsPageSize, setGrantsPageSize] = useState(DEFAULT_PAGE_SIZE);
  const [loading, setLoading] = useState(true);
  const [busyId, setBusyId] = useState<string | null>(null);

  const [sessions, setSessions] = useState<ShareSessionView[]>([]);
  const [sessionsTotal, setSessionsTotal] = useState(0);
  const [sessionsPage, setSessionsPage] = useState(1);
  const [sessionsPageSize, setSessionsPageSize] = useState(DEFAULT_PAGE_SIZE);
  const [sessionsLoading, setSessionsLoading] = useState(true);
  const [busySessionId, setBusySessionId] = useState<string | null>(null);
  const [watchTarget, setWatchTarget] = useState<ShareSessionView | null>(null);

  const [err, setErr] = useState<string | null>(null);

  // Guards the auto-refresh interval against overlapping requests: a manual
  // Refresh or an in-flight action should not race a timer tick.
  const busyRef = useRef(false);
  // Tracks current page/size without retriggering the fetch callbacks, so the
  // poll interval always re-fetches whatever page the operator is looking at.
  const pageRef = useRef({ grantsPage, grantsPageSize, sessionsPage, sessionsPageSize });
  pageRef.current = { grantsPage, grantsPageSize, sessionsPage, sessionsPageSize };

  const fetchGrants = (page: number, pageSize: number) => {
    setLoading(true);
    return listGrants(page, pageSize)
      .then((r) => {
        setGrants(r.grants);
        setGrantsTotal(r.total);
        setGrantsPage(r.page);
        setGrantsPageSize(r.per_page);
      })
      .catch((e: unknown) => setErr(e instanceof Error ? e.message : String(e)))
      .finally(() => setLoading(false));
  };

  const fetchSessions = (page: number, pageSize: number) => {
    setSessionsLoading(true);
    return listShareSessions(page, pageSize)
      .then((r) => {
        setSessions(r.sessions);
        setSessionsTotal(r.total);
        setSessionsPage(r.page);
        setSessionsPageSize(r.per_page);
      })
      .catch((e: unknown) => setErr(e instanceof Error ? e.message : String(e)))
      .finally(() => setSessionsLoading(false));
  };

  const refreshAll = () => {
    const { grantsPage: gp, grantsPageSize: gs, sessionsPage: sp, sessionsPageSize: ss } = pageRef.current;
    return Promise.all([fetchGrants(gp, gs), fetchSessions(sp, ss)]);
  };

  useEffect(() => {
    refreshAll();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    const id = setInterval(() => {
      if (busyRef.current) {
        return;
      }
      refreshAll();
    }, REFRESH_INTERVAL_MS);
    return () => clearInterval(id);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const decide = (grant: JitGrantView, decision: JitGrantDecision) => {
    busyRef.current = true;
    setBusyId(grant.id);
    setErr(null);
    decideGrant(grant.id, decision)
      .then(() => fetchGrants(grantsPage, grantsPageSize))
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

  const doDeleteGrant = (grant: JitGrantView) => {
    busyRef.current = true;
    setBusyId(grant.id);
    setErr(null);
    deleteGrant(grant.id)
      .then(() => fetchGrants(grantsPage, grantsPageSize))
      .catch((e: unknown) => setErr(e instanceof Error ? e.message : String(e)))
      .finally(() => {
        busyRef.current = false;
        setBusyId(null);
      });
  };

  const confirmDeleteGrant = (grant: JitGrantView) => {
    Modal.confirm({
      title: `Delete this grant for ${grant.resource.name}?`,
      content: 'This permanently removes the grant and its audit record. This cannot be undone.',
      okText: 'Delete',
      okButtonProps: { danger: true },
      onOk: () => doDeleteGrant(grant),
    });
  };

  const confirmPurge = () => {
    Modal.confirm({
      title: 'Delete all finished grants?',
      content: 'Permanently removes every denied, revoked, and expired grant (and their audit records). Active grants are never touched. This cannot be undone.',
      okText: 'Delete all finished',
      okButtonProps: { danger: true },
      onOk: () => {
        busyRef.current = true;
        setErr(null);
        return purgeGrants()
          .then(() => fetchGrants(1, grantsPageSize))
          .catch((e: unknown) => setErr(e instanceof Error ? e.message : String(e)))
          .finally(() => {
            busyRef.current = false;
          });
      },
    });
  };

  const doKill = (session: ShareSessionView) => {
    busyRef.current = true;
    setBusySessionId(session.grant_id);
    setErr(null);
    killShareSession(session.grant_id)
      .then(() => fetchSessions(sessionsPage, sessionsPageSize))
      .catch((e: unknown) => setErr(e instanceof Error ? e.message : String(e)))
      .finally(() => {
        busyRef.current = false;
        setBusySessionId(null);
      });
  };

  const confirmKill = (session: ShareSessionView) => {
    Modal.confirm({
      title: `Kill this session?`,
      content:
        'This revokes the access link and terminates the guest\'s session right now. Anyone watching it is disconnected too.',
      okText: 'Kill',
      okButtonProps: { danger: true },
      onOk: () => doKill(session),
    });
  };

  /** "live" once redeemed and still running, "ended" once redeemed and gone, "not redeemed" beforehand. */
  function sessionStatus(r: ShareSessionView): { label: string; color: string } {
    if (r.session_alive) {
      return { label: 'live', color: 'green' };
    }
    if (r.redemptions > 0) {
      return { label: 'ended', color: 'default' };
    }
    return { label: 'not redeemed', color: 'orange' };
  }

  /** Reason a disabled Watch button is disabled, or '' when it's enabled. */
  function watchDisabledReason(r: ShareSessionView): string {
    if (!r.observable) {
      return 'This host has no multiplexer, so this session can never be watched';
    }
    if (!r.session_alive) {
      return 'No guest session is currently running';
    }
    return '';
  }

  const sessionColumns = [
    {
      title: 'Resource',
      key: 'resource',
      render: (_: unknown, r: ShareSessionView) => (
        <span>
          {r.resource.name} <Typography.Text type="secondary">({r.resource.provider})</Typography.Text>
        </span>
      ),
    },
    {
      title: 'Guest',
      key: 'recipient',
      render: (_: unknown, r: ShareSessionView) => r.recipient || <Typography.Text type="secondary">anyone with the link</Typography.Text>,
    },
    {
      title: 'Status',
      key: 'status',
      render: (_: unknown, r: ShareSessionView) => {
        const s = sessionStatus(r);
        return <Tag color={s.color}>{s.label}</Tag>;
      },
    },
    {
      title: 'Observers',
      key: 'observers',
      render: (_: unknown, r: ShareSessionView) => (
        <Tag color={r.observers > 0 ? 'blue' : 'default'}>{r.observers}</Tag>
      ),
    },
    {
      title: 'Expires',
      key: 'expires_at',
      render: (_: unknown, r: ShareSessionView) => (r.expires_at ? new Date(r.expires_at).toLocaleString() : '—'),
    },
    {
      title: 'Actions',
      key: 'actions',
      render: (_: unknown, r: ShareSessionView) => {
        const watchReason = watchDisabledReason(r);
        const watchButton = (
          <Button size="small" disabled={!!watchReason} onClick={() => setWatchTarget(r)}>
            Watch
          </Button>
        );
        return (
          <Space size={8}>
            {watchReason ? <Tooltip title={watchReason}>{watchButton}</Tooltip> : watchButton}
            <Button
              size="small"
              danger
              loading={busySessionId === r.grant_id}
              disabled={busySessionId !== null && busySessionId !== r.grant_id}
              onClick={() => confirmKill(r)}
            >
              Kill
            </Button>
          </Space>
        );
      },
    },
  ];

  const grantColumns = [
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
        const rowDisabled = busyId !== null && !rowBusy;
        const terminal = isTerminalGrant(r);
        const deleteButton = (
          <Button size="small" danger loading={rowBusy} disabled={rowDisabled || !terminal} onClick={() => confirmDeleteGrant(r)}>
            Delete
          </Button>
        );
        return (
          <Space size={8}>
            {r.status === 'pending' && (
              <>
                <Button size="small" type="primary" loading={rowBusy} disabled={rowDisabled} onClick={() => confirmDecide(r, 'approve')}>
                  Approve
                </Button>
                <Button size="small" loading={rowBusy} disabled={rowDisabled} onClick={() => confirmDecide(r, 'deny')}>
                  Deny
                </Button>
              </>
            )}
            {r.status === 'approved' && (
              <Button size="small" danger loading={rowBusy} disabled={rowDisabled} onClick={() => confirmDecide(r, 'revoke')}>
                Revoke
              </Button>
            )}
            {terminal ? deleteButton : <Tooltip title="Revoke this grant before it can be deleted">{deleteButton}</Tooltip>}
          </Space>
        );
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

      <Card
        size="small"
        title={<Space><span>Guest sessions</span><Button size="small" onClick={() => fetchSessions(sessionsPage, sessionsPageSize)}>Refresh</Button></Space>}
        style={{ marginBottom: 16 }}
      >
        <Typography.Paragraph type="secondary" style={{ marginBottom: 12 }}>
          Working sessions redeemed from an access link. Watch opens a read-only live view of what the
          guest is doing right now; Kill revokes the link and terminates the guest's session.
        </Typography.Paragraph>
        <Table
          rowKey={(r) => r.grant_id}
          size="small"
          loading={sessionsLoading}
          columns={sessionColumns}
          dataSource={sessions}
          scroll={{ x: 'max-content' }}
          locale={{ emptyText: 'No guest has redeemed an access request yet' }}
          pagination={{
            current: sessionsPage,
            pageSize: sessionsPageSize,
            total: sessionsTotal,
            showSizeChanger: true,
            onChange: (page, pageSize) => fetchSessions(page, pageSize),
          }}
        />
      </Card>

      <Card
        size="small"
        title={
          <Space>
            <span>Grants</span>
            <Button size="small" onClick={() => fetchGrants(grantsPage, grantsPageSize)}>Refresh</Button>
            <Button size="small" danger onClick={confirmPurge}>Delete all finished</Button>
          </Space>
        }
      >
        <Table
          rowKey={(r) => r.id}
          size="small"
          loading={loading}
          columns={grantColumns}
          dataSource={grants}
          scroll={{ x: 'max-content' }}
          locale={{ emptyText: 'No access requests yet' }}
          pagination={{
            current: grantsPage,
            pageSize: grantsPageSize,
            total: grantsTotal,
            showSizeChanger: true,
            onChange: (page, pageSize) => fetchGrants(page, pageSize),
          }}
        />
      </Card>

      <ShareWatchModal
        grantId={watchTarget?.grant_id ?? null}
        resourceName={watchTarget?.resource.name}
        onClose={() => setWatchTarget(null)}
      />
    </div>
  );
}
