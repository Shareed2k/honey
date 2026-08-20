import { useCallback, useEffect, useState } from 'react';
import { Alert, Button, Card, Input, Result, Space, Tag, Typography } from 'antd';
import { getRedeemStatus, redeemCert } from './api/jit';
import type { JitCapability, RedeemCertResponse, RedeemStatus } from './api/jit';
import { JitTerminal } from './JitTerminal';

export type RedeemAccessViewProps = {
  code: string;
};

const CAPABILITY_LABELS: Record<JitCapability, string> = {
  shell: 'Shell',
  exec: 'Exec',
  tunnel: 'Tunnel',
};

// Human explanation for an inactive link, keyed by the server's `reason`.
const INACTIVE_MESSAGES: Record<string, { title: string; sub: string }> = {
  pending: {
    title: 'Awaiting approval',
    sub: 'This access request needs approval before the link becomes usable.',
  },
  denied: { title: 'Request denied', sub: 'An approver denied this access request.' },
  revoked: { title: 'Link revoked', sub: 'This access link was revoked.' },
  expired: { title: 'Link expired', sub: 'This access link is past its expiry window.' },
  exhausted: { title: 'Link used up', sub: 'This link has reached its redemption limit.' },
  not_started: { title: 'Not active yet', sub: 'This link is not active yet.' },
  inactive: { title: 'This link is no longer active', sub: '' },
};

function inactiveMessage(s: RedeemStatus): { title: string; sub: string } {
  return INACTIVE_MESSAGES[s.reason ?? ''] ?? INACTIVE_MESSAGES[s.status] ?? INACTIVE_MESSAGES.inactive;
}

// RedeemAccessView is the standalone, unauthenticated page a share-link
// recipient lands on. It carries no honey session: every call it makes
// (getRedeemStatus, redeemCert, the terminal WebSocket) authenticates with the
// code alone.
export function RedeemAccessView({ code }: RedeemAccessViewProps) {
  const [status, setStatus] = useState<RedeemStatus | null>(null);
  const [loading, setLoading] = useState(true);
  const [loadErr, setLoadErr] = useState<string | null>(null);

  const [showTerminal, setShowTerminal] = useState(false);

  const [publicKey, setPublicKey] = useState('');
  const [certBusy, setCertBusy] = useState(false);
  const [certErr, setCertErr] = useState<string | null>(null);
  const [cert, setCert] = useState<RedeemCertResponse | null>(null);
  const [copiedField, setCopiedField] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    setLoadErr(null);
    getRedeemStatus(code)
      .then((s) => {
        if (!cancelled) {
          setStatus(s);
        }
      })
      .catch((e: unknown) => {
        if (!cancelled) {
          setLoadErr(e instanceof Error ? e.message : String(e));
        }
      })
      .finally(() => {
        if (!cancelled) {
          setLoading(false);
        }
      });
    return () => {
      cancelled = true;
    };
  }, [code]);

  // While a grant is pending approval, poll until it flips to active (or gets
  // denied/revoked) so the recipient doesn't have to manually reload the page.
  // Deps are the primitive fields (not `status` itself) so the effect only
  // re-runs on an actual state transition, not on every unrelated re-render.
  const statusStatus = status?.status;
  const statusActive = status?.active ?? false;
  useEffect(() => {
    if (statusStatus !== 'pending' || statusActive) {
      return;
    }
    let cancelled = false;
    let inFlight = false;
    const id = setInterval(() => {
      if (inFlight) {
        return;
      }
      inFlight = true;
      getRedeemStatus(code)
        .then((s) => {
          if (!cancelled) {
            setStatus(s);
          }
        })
        .catch(() => {
          // Transient poll failure — keep the last known status and retry
          // on the next tick rather than surfacing an error banner.
        })
        .finally(() => {
          inFlight = false;
        });
    }, 4000);
    return () => {
      cancelled = true;
      clearInterval(id);
    };
  }, [code, statusStatus, statusActive]);

  const copy = useCallback((field: string, value: string) => {
    navigator.clipboard.writeText(value).then(
      () => {
        setCopiedField(field);
        setTimeout(() => setCopiedField((f) => (f === field ? null : f)), 1500);
      },
      () => setCertErr('Could not copy to clipboard'),
    );
  }, []);

  const onGetCert = async () => {
    setCertErr(null);
    setCertBusy(true);
    try {
      const resp = await redeemCert(code, publicKey.trim());
      setCert(resp);
    } catch (e) {
      setCertErr(e instanceof Error ? e.message : String(e));
    } finally {
      setCertBusy(false);
    }
  };

  const notFound = !loading && (!!loadErr || !status);
  const inactive = !loading && !!status && (status.status !== 'approved' || !status.active);
  const active = !loading && !!status && status.status === 'approved' && status.active;

  // Once the recipient opens the terminal, give it the whole viewport — a
  // terminal in a narrow card is cramped. A slim bar keeps the resource
  // context and a way back to the lobby.
  if (showTerminal && status) {
    return (
      <div
        style={{
          position: 'fixed',
          inset: 0,
          display: 'flex',
          flexDirection: 'column',
          background: '#0f1115',
          zIndex: 1000,
        }}
      >
        <div
          style={{
            display: 'flex',
            alignItems: 'center',
            gap: 12,
            padding: '8px 16px',
            borderBottom: '1px solid #2a3140',
            flex: '0 0 auto',
          }}
        >
          <Typography.Text strong style={{ color: '#e8e8e8' }}>
            {status.resource.name}{' '}
            <Typography.Text type="secondary">({status.resource.provider})</Typography.Text>
          </Typography.Text>
          {status.expires_at ? (
            <Typography.Text type="secondary" style={{ fontSize: 12 }}>
              expires {new Date(status.expires_at).toLocaleString()}
            </Typography.Text>
          ) : null}
          <span style={{ flex: 1 }} />
          <Button size="small" onClick={() => setShowTerminal(false)}>
            Close
          </Button>
        </div>
        <div style={{ flex: 1, minHeight: 0, padding: 8 }}>
          <JitTerminal code={code} />
        </div>
      </div>
    );
  }

  return (
    <div style={{ minHeight: '100vh', display: 'flex', flexDirection: 'column', background: '#0f1115' }}>
      <header
        style={{
          display: 'flex',
          alignItems: 'center',
          gap: 10,
          padding: '12px 20px',
          borderBottom: '1px solid #2a3140',
          flex: '0 0 auto',
        }}
      >
        <svg viewBox="0 0 120 120" width="20" height="20" aria-hidden style={{ flexShrink: 0 }}>
          <defs>
            <linearGradient id="honeyGradAccess" x1="0%" y1="0%" x2="100%" y2="100%">
              <stop offset="0%" stopColor="#FFC107" />
              <stop offset="100%" stopColor="#F57C00" />
            </linearGradient>
          </defs>
          <polygon points="60,10 105,35 105,85 60,110 15,85 15,35" fill="url(#honeyGradAccess)" />
          <polygon points="60,20 95,40 95,80 60,100 25,80 25,40" fill="#14171c" />
        </svg>
        <Typography.Text strong style={{ color: '#e6e6e6', fontSize: 15 }}>
          honey — access
        </Typography.Text>
      </header>
      <main
        style={{
          flex: 1,
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
          padding: 24,
        }}
      >
        <div style={{ width: '100%', maxWidth: 640 }}>
          <Card title="Access link" loading={loading}>
        {notFound ? (
          <Result status="404" title="Link not found" subTitle="This link is invalid or expired." />
        ) : null}

        {inactive && status ? (
          <Result status="info" title={inactiveMessage(status).title} subTitle={inactiveMessage(status).sub} />
        ) : null}

        {active && status ? (
          <Space direction="vertical" size={16} style={{ width: '100%' }}>
            <div>
              <Typography.Title level={5} style={{ marginBottom: 4 }}>
                {status.resource.name}{' '}
                <Typography.Text type="secondary">({status.resource.provider})</Typography.Text>
              </Typography.Title>
              <Space size={4} wrap>
                {status.capabilities.map((c) => (
                  <Tag key={c}>{CAPABILITY_LABELS[c] ?? c}</Tag>
                ))}
              </Space>
              {status.expires_at ? (
                <Typography.Paragraph type="secondary" style={{ marginTop: 8, marginBottom: 0 }}>
                  Expires {new Date(status.expires_at).toLocaleString()}
                </Typography.Paragraph>
              ) : null}
            </div>

            {!status.offers.web && !status.offers.cert ? (
              <Alert
                type="warning"
                showIcon
                message="No redeemable action for this link"
                description="This link's capabilities are delivered via an SSH certificate, but it was issued with browser-terminal-only delivery. Ask the issuer to re-create it with 'SSH certificate' (or 'Both') delivery."
              />
            ) : null}

            {status.offers.web ? (
              <div>
                <Button type="primary" onClick={() => setShowTerminal(true)}>
                  Open terminal in browser
                </Button>
              </div>
            ) : null}

            {status.offers.cert ? (
              <div>
                <Typography.Title level={5}>SSH certificate</Typography.Title>
                {!cert ? (
                  <Space direction="vertical" style={{ width: '100%' }}>
                    <label htmlFor="jit-pubkey" style={{ display: 'block' }}>
                      <Typography.Text>Paste your SSH public key</Typography.Text>
                    </label>
                    <Input.TextArea
                      id="jit-pubkey"
                      rows={3}
                      value={publicKey}
                      onChange={(e) => setPublicKey(e.target.value)}
                      placeholder="ssh-ed25519 AAAA…"
                    />
                    {certErr ? (
                      <Alert type="error" message={certErr} closable onClose={() => setCertErr(null)} />
                    ) : null}
                    <Button
                      type="primary"
                      loading={certBusy}
                      disabled={!publicKey.trim()}
                      onClick={() => void onGetCert()}
                    >
                      Get certificate
                    </Button>
                  </Space>
                ) : (
                  <Space direction="vertical" size={8} style={{ width: '100%' }}>
                    <div>
                      <Typography.Text style={{ display: 'block', marginBottom: 4 }}>
                        Certificate
                      </Typography.Text>
                      <Input.TextArea readOnly rows={3} value={cert.cert} aria-label="Signed certificate" />
                      <Button size="small" style={{ marginTop: 4 }} onClick={() => copy('cert', cert.cert)}>
                        {copiedField === 'cert' ? 'Copied' : 'Copy'}
                      </Button>
                    </div>
                    <div>
                      <Typography.Text style={{ display: 'block', marginBottom: 4 }}>CA</Typography.Text>
                      <Input.TextArea readOnly rows={2} value={cert.ca} aria-label="Certificate authority" />
                      <Button size="small" style={{ marginTop: 4 }} onClick={() => copy('ca', cert.ca)}>
                        {copiedField === 'ca' ? 'Copied' : 'Copy'}
                      </Button>
                    </div>
                    <Alert
                      type="info"
                      message="Ready to run"
                      description={
                        <Space direction="vertical" size={4} style={{ width: '100%' }}>
                          {status.capabilities.includes('shell') || status.capabilities.includes('exec') ? (
                            <code>
                              {`ssh -i <key> -i <key>-cert.pub ${cert.principals[0]}@<gateway> -p 12222 ${status.resource.name}`}
                            </code>
                          ) : null}
                          {status.capabilities.includes('tunnel') ? (
                            <code>
                              {`ssh -i <key> -i <key>-cert.pub -N -L <LOCAL_PORT>:${status.resource.name}:<REMOTE_PORT> ${cert.principals[0]}@<gateway> -p 12222`}
                            </code>
                          ) : null}
                        </Space>
                      }
                    />
                  </Space>
                )}
              </div>
            ) : null}
          </Space>
        ) : null}
          </Card>
        </div>
      </main>
    </div>
  );
}
