import { useEffect, useMemo, useRef, useState } from 'react';
import { Alert, Button, Card, Checkbox, Form, InputNumber, Select, Space, Typography, Input } from 'antd';
import { startAgentTransferStream } from '../api/files';
import { type AgentTransferBackendRef, type AgentTransferCloud, type AgentTransferEvent } from '../api/types/files';
import { recordKey } from '../HostPicker';
import type { HostRecord } from '../HostPicker';

type BackendRow = { kind: string; name: string; hint: string };

interface Props {
  records: HostRecord[];
  backends: BackendRow[];
}

export function FilesTab({ records, backends }: Props) {
  const [transferSourceHostKey, setTransferSourceHostKey] = useState('');
  const [transferDestHostKey, setTransferDestHostKey] = useState('');
  const [transferSourcePath, setTransferSourcePath] = useState('/tmp/source.bin');
  const [transferDestPath, setTransferDestPath] = useState('/tmp/dest.bin');
  const [transferCloud, setTransferCloud] = useState<AgentTransferCloud>({
    provider: 's3',
    bucket: '',
    prefix: 'honey-transfer',
    object: '',
    region: '',
    endpoint: '',
  });
  const [transferBackendRefValue, setTransferBackendRefValue] = useState('');
  const [transferKeepObject, setTransferKeepObject] = useState(false);
  const [transferMaxRetries, setTransferMaxRetries] = useState(2);
  const [transferBusy, setTransferBusy] = useState(false);
  const [transferErr, setTransferErr] = useState<string | null>(null);
  const [transferEvents, setTransferEvents] = useState<AgentTransferEvent[]>([]);
  const [sshUser, setSshUser] = useState('');
  const transferAbortRef = useRef<AbortController | null>(null);

  const transferHostOptions = useMemo(() => records.filter((r) => !!r.primary_ip.trim()), [records]);
  const transferBackendKind = transferCloud.provider === 'googlecloudstorage' ? 'gcp' : 'aws';
  const transferBackendOptions = useMemo(
    () =>
      backends.filter((b) => b.kind.toLowerCase() === transferBackendKind && b.name.trim() !== ''),
    [backends, transferBackendKind],
  );

  useEffect(() => {
    return () => {
      transferAbortRef.current?.abort();
      transferAbortRef.current = null;
    };
  }, []);

  useEffect(() => {
    if (transferHostOptions.length === 0) {
      setTransferSourceHostKey('');
      setTransferDestHostKey('');
      return;
    }
    if (!transferSourceHostKey) {
      setTransferSourceHostKey(recordKey(transferHostOptions[0]));
    }
    if (!transferDestHostKey) {
      const second = transferHostOptions[1] ?? transferHostOptions[0];
      setTransferDestHostKey(recordKey(second));
    }
  }, [transferHostOptions, transferSourceHostKey, transferDestHostKey]);

  useEffect(() => {
    if (transferBackendOptions.length === 0) {
      setTransferBackendRefValue('');
      return;
    }
    if (transferBackendRefValue === '') {
      return;
    }
    const stillValid = transferBackendOptions.some((b) => `${b.kind}:${b.name}` === transferBackendRefValue);
    if (!stillValid) {
      const first = transferBackendOptions[0];
      setTransferBackendRefValue(`${first.kind}:${first.name}`);
    }
  }, [transferBackendOptions, transferBackendRefValue]);

  const submitAgentTransfer = async () => {
    const sourceHost = transferHostOptions.find((r) => recordKey(r) === transferSourceHostKey);
    const destHost = transferHostOptions.find((r) => recordKey(r) === transferDestHostKey);
    if (!sourceHost || !destHost) {
      setTransferErr('Select both source and destination hosts.');
      return;
    }
    if (!transferSourcePath.trim() || !transferDestPath.trim()) {
      setTransferErr('Source path and destination path are required.');
      return;
    }
    if (!transferCloud.provider.trim() || !transferCloud.bucket.trim()) {
      setTransferErr('Cloud provider and bucket are required.');
      return;
    }
    let backendRef: AgentTransferBackendRef | undefined;
    if (transferBackendRefValue) {
      const split = transferBackendRefValue.split(':');
      if (split.length >= 2) {
        const kind = split[0]?.trim();
        const name = split.slice(1).join(':').trim();
        if (kind && name) {
          backendRef = { kind, name };
        }
      }
    }
    setTransferBusy(true);
    setTransferErr(null);
    setTransferEvents([]);
    const abortController = new AbortController();
    transferAbortRef.current = abortController;
    try {
      await startAgentTransferStream(
        {
          ssh_user: sshUser.trim(),
          source_record: sourceHost,
          source_path: transferSourcePath.trim(),
          dest_record: destHost,
          dest_path: transferDestPath.trim(),
          cloud: {
            provider: transferCloud.provider.trim(),
            bucket: transferCloud.bucket.trim(),
            prefix: transferCloud.prefix?.trim() || undefined,
            object: transferCloud.object?.trim() || undefined,
            region: transferCloud.region?.trim() || undefined,
            endpoint: transferCloud.endpoint?.trim() || undefined,
          },
          cloud_backend_ref: backendRef,
          keep_object: transferKeepObject,
          max_retries: transferMaxRetries,
        },
        (ev) => setTransferEvents((prev) => [...prev, ev]),
        abortController.signal,
      );
    } catch (e) {
      if (e instanceof Error && e.name === 'AbortError') {
        setTransferErr('Transfer aborted by user.');
      } else {
        setTransferErr(e instanceof Error ? e.message : String(e));
      }
    } finally {
      if (transferAbortRef.current === abortController) {
        transferAbortRef.current = null;
      }
      setTransferBusy(false);
    }
  };

  const abortAgentTransfer = () => {
    const ctrl = transferAbortRef.current;
    if (!ctrl) return;
    ctrl.abort();
    transferAbortRef.current = null;
  };

  const hostSelectOptions = transferHostOptions.map((r) => ({
    value: recordKey(r),
    label: `${r.name} (${r.primary_ip})`,
  }));

  const backendSelectOptions = [
    { value: '', label: 'None (use Honey server default SDK auth chain)' },
    ...transferBackendOptions.map((b) => ({
      value: `${b.kind}:${b.name}`,
      label: `${b.kind}: ${b.name}${b.hint ? ` (${b.hint})` : ''}`,
    })),
  ];

  const cloudProviderOptions = [
    { value: 's3', label: 's3' },
    { value: 'googlecloudstorage', label: 'googlecloudstorage' },
  ];

  return (
    <div style={{ width: '100%', display: 'flex', flexDirection: 'column', gap: 8 }}>
      <Typography.Text type="secondary" style={{ fontSize: '0.85rem' }}>
        Transfer path: source host uploads to cloud object, destination host downloads from cloud
        using ephemeral agent over SSH control-plane. Cloud credentials are resolved only on Honey,
        and remotes receive encrypted short-lived credential envelopes.
      </Typography.Text>

      <Card size="small" title="Hosts">
        <Form layout="vertical">
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(220px, 1fr))', gap: '8px 12px' }}>
            <Form.Item label="Source host" style={{ marginBottom: 0 }}>
              <Select
                value={transferSourceHostKey || undefined}
                onChange={setTransferSourceHostKey}
                options={hostSelectOptions}
                placeholder="Select source host"
              />
            </Form.Item>
            <Form.Item label="Destination host" style={{ marginBottom: 0 }}>
              <Select
                value={transferDestHostKey || undefined}
                onChange={setTransferDestHostKey}
                options={hostSelectOptions}
                placeholder="Select destination host"
              />
            </Form.Item>
          </div>
        </Form>
      </Card>

      <Card size="small" title="Paths">
        <Form layout="vertical">
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(220px, 1fr))', gap: '8px 12px' }}>
            <Form.Item label="Source path" style={{ marginBottom: 0 }}>
              <Input
                value={transferSourcePath}
                onChange={(e) => setTransferSourcePath(e.target.value)}
              />
            </Form.Item>
            <Form.Item label="Destination path" style={{ marginBottom: 0 }}>
              <Input
                value={transferDestPath}
                onChange={(e) => setTransferDestPath(e.target.value)}
              />
            </Form.Item>
          </div>
        </Form>
      </Card>

      <Card size="small" title="Cloud staging">
        <Form layout="vertical">
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(160px, 1fr))', gap: '8px 12px' }}>
            <Form.Item label="Provider" style={{ marginBottom: 0 }}>
              <Select
                value={transferCloud.provider}
                onChange={(val) => setTransferCloud((prev) => ({ ...prev, provider: val }))}
                options={cloudProviderOptions}
              />
            </Form.Item>
            <Form.Item label="Bucket" style={{ marginBottom: 0 }}>
              <Input
                value={transferCloud.bucket}
                onChange={(e) => setTransferCloud((prev) => ({ ...prev, bucket: e.target.value }))}
              />
            </Form.Item>
            <Form.Item label="Prefix" style={{ marginBottom: 0 }}>
              <Input
                value={transferCloud.prefix || ''}
                onChange={(e) => setTransferCloud((prev) => ({ ...prev, prefix: e.target.value }))}
              />
            </Form.Item>
            <Form.Item label="Object key (optional)" style={{ marginBottom: 0 }}>
              <Input
                value={transferCloud.object || ''}
                onChange={(e) => setTransferCloud((prev) => ({ ...prev, object: e.target.value }))}
              />
            </Form.Item>
            <Form.Item label="Region (optional)" style={{ marginBottom: 0 }}>
              <Input
                value={transferCloud.region || ''}
                onChange={(e) => setTransferCloud((prev) => ({ ...prev, region: e.target.value }))}
              />
            </Form.Item>
            <Form.Item label="Endpoint (optional)" style={{ marginBottom: 0 }}>
              <Input
                value={transferCloud.endpoint || ''}
                onChange={(e) => setTransferCloud((prev) => ({ ...prev, endpoint: e.target.value }))}
              />
            </Form.Item>
          </div>
        </Form>
      </Card>

      <Card size="small" title="Credentials">
        <Form layout="vertical">
          <Form.Item label="Honey credential source (for encrypted envelopes)" style={{ marginBottom: 0 }}>
            <Select
              value={transferBackendRefValue}
              onChange={setTransferBackendRefValue}
              options={backendSelectOptions}
            />
            {transferBackendOptions.length === 0 ? (
              <Typography.Text type="secondary" style={{ fontSize: '0.8rem', display: 'block', marginTop: 4 }}>
                No named {transferBackendKind} backend found in config. Honey will use its default SDK credential chain.
              </Typography.Text>
            ) : null}
          </Form.Item>
        </Form>
      </Card>

      <Space wrap align="center">
        <Checkbox
          checked={transferKeepObject}
          onChange={(e) => setTransferKeepObject(e.target.checked)}
        >
          Keep cloud object after transfer
        </Checkbox>
        <Space size={4}>
          <Typography.Text type="secondary">Retries</Typography.Text>
          <InputNumber
            min={1}
            max={5}
            value={transferMaxRetries}
            onChange={(val) => setTransferMaxRetries(val ?? 1)}
            style={{ width: 72 }}
          />
        </Space>
        <Space size={4}>
          <Typography.Text type="secondary">SSH user</Typography.Text>
          <Input
            value={sshUser}
            onChange={(e) => setSshUser(e.target.value)}
            style={{ width: 140 }}
          />
        </Space>
        <Button
          type="primary"
          loading={transferBusy}
          disabled={transferBusy || transferHostOptions.length === 0}
          onClick={() => void submitAgentTransfer()}
        >
          {transferBusy ? 'Transferring…' : 'Start A → cloud → B'}
        </Button>
        <Button disabled={!transferBusy} onClick={() => abortAgentTransfer()}>
          Abort
        </Button>
      </Space>

      {transferErr ? (
        <Alert type="error" showIcon message={transferErr} />
      ) : null}

      <Card size="small" title="Transfer events" styles={{ body: { padding: '0.4rem 0.55rem' } }}>
        <div style={{ maxHeight: '38vh', overflow: 'auto' }}>
          {transferEvents.length === 0 ? (
            <Typography.Text type="secondary">No events yet.</Typography.Text>
          ) : (
            transferEvents.map((ev, i) => (
              <div key={`${ev.timestamp}-${i}`} style={{ marginBottom: 3 }}>
                <Typography.Text
                  type={ev.success ? undefined : 'danger'}
                  style={{ fontFamily: 'monospace', fontSize: '0.78rem' }}
                >
                  [{ev.timestamp}] {ev.stage}
                  {ev.host ? ` @ ${ev.host}` : ''} :: {ev.success ? 'ok' : 'failed'}
                  {ev.attempt ? ` (attempt ${ev.attempt})` : ''}
                  {ev.message ? ` :: ${ev.message}` : ''}
                  {ev.error ? ` :: ${ev.error}` : ''}
                </Typography.Text>
              </div>
            ))
          )}
        </div>
      </Card>
    </div>
  );
}
