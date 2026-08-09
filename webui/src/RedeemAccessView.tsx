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

  return (
    <div style={{ maxWidth: 640, margin: '0 auto', paddingTop: 48, paddingBottom: 48 }}>
      <Card title="honey — access" loading={loading}>
        {notFound ? (
          <Result status="404" title="Link not found" subTitle="This link is invalid or expired." />
        ) : null}

        {inactive && status ? (
          <Result
            status="info"
            title={status.status === 'pending' ? 'Awaiting approval' : 'This link is no longer active'}
            subTitle={
              status.status === 'pending'
                ? 'Someone needs to approve this access request before the link becomes usable.'
                : `Status: ${status.status}`
            }
          />
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

            {status.offers.web ? (
              <div>
                {!showTerminal ? (
                  <Button type="primary" onClick={() => setShowTerminal(true)}>
                    Open terminal in browser
                  </Button>
                ) : (
                  <JitTerminal code={code} />
                )}
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
                        <code>
                          {`ssh -i <key> -i <key>-cert.pub ${cert.principals[0]}@<gateway> -p 12222 ${status.resource.name}`}
                        </code>
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
  );
}
