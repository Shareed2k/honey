import React, { useCallback, useEffect, useMemo, useState } from 'react';
import {
  Alert,
  Button,
  Descriptions,
  Input,
  Modal,
  Select,
  Space,
  Spin,
  Switch,
  Table,
  Tag,
  Typography,
} from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { type AppConfig } from '../api/types/proxy';
import { type HostExecResultRow } from '../api/types/exec';
import {
  debugWebhook,
  listWebhookDeliveries,
  type WebhookDebugResponse,
  type WebhookDelivery,
} from '../api/webhooks';

function formatResults(results: HostExecResultRow[]): string {
  return results
    .map((r) => `[${r.Name || '?'}] ${r.ErrMsg ? 'ERR: ' + r.ErrMsg : (r.Output ?? '')}`)
    .join('\n');
}

function outcomeColor(outcome: string): string {
  switch (outcome) {
    case 'executed':
    case 'completed':
      return 'green';
    case 'queued':
      return 'processing';
    case 'dry_run':
      return 'blue';
    case 'duplicate':
      return 'gold';
    case 'unauthorized':
    case 'error':
    case 'failed':
      return 'red';
    default:
      return 'default';
  }
}

function sourceColor(source: string): string {
  switch (source) {
    case 'live':
      return 'geekblue';
    case 'test':
      return 'purple';
    case 'dry_run':
      return 'cyan';
    default:
      return 'default';
  }
}

export function WebhookDebugModal({
  app,
  open,
  onClose,
}: {
  app: AppConfig | null;
  open: boolean;
  onClose: () => void;
}) {
  const webhooks = useMemo(() => app?.webhooks ?? [], [app]);
  const [selected, setSelected] = useState<string>('');
  const [payload, setPayload] = useState<string>('{}');
  const [execute, setExecute] = useState<boolean>(false);
  const [sending, setSending] = useState<boolean>(false);
  const [error, setError] = useState<string>('');
  const [result, setResult] = useState<WebhookDebugResponse | null>(null);
  const [deliveries, setDeliveries] = useState<WebhookDelivery[]>([]);
  // After an async ("queued") send, auto-refresh deliveries until the matching
  // row enriches (its recording finishes), so results land in the row.
  const [pendingExecId, setPendingExecId] = useState<string | null>(null);
  const [polling, setPolling] = useState(false);

  useEffect(() => {
    if (open && webhooks.length > 0) {
      setSelected((prev) => (prev && webhooks.includes(prev) ? prev : webhooks[0]));
    }
  }, [open, webhooks]);

  const refreshDeliveries = useCallback(async () => {
    if (!app || !selected) return;
    try {
      setDeliveries(await listWebhookDeliveries(app.name, selected, 20));
    } catch {
      /* non-fatal: leave the existing list */
    }
  }, [app, selected]);

  useEffect(() => {
    if (open) {
      setResult(null);
      setError('');
      setPendingExecId(null);
      setPolling(false);
      void refreshDeliveries();
    }
  }, [open, selected, refreshDeliveries]);

  const send = async () => {
    if (!app || !selected) return;
    let parsed: unknown;
    try {
      parsed = payload.trim() === '' ? {} : JSON.parse(payload);
    } catch (e) {
      setError(`Invalid JSON payload: ${e instanceof Error ? e.message : String(e)}`);
      return;
    }
    setError('');
    setSending(true);
    setPendingExecId(null);
    setPolling(false);
    try {
      const resp = await debugWebhook(app.name, selected, parsed, execute);
      setResult(resp);
      await refreshDeliveries();
      if (resp.outcome === 'queued' && resp.exec_id) {
        setPendingExecId(resp.exec_id);
      }
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setSending(false);
    }
  };

  // After a queued (async) send, auto-refresh deliveries until the matching row
  // enriches server-side (recording finished) so its results + outcome appear.
  useEffect(() => {
    if (!pendingExecId || !app || !selected) return;
    let cancelled = false;
    let timer: ReturnType<typeof setTimeout>;
    let tries = 0;
    setPolling(true);
    const tick = async () => {
      if (cancelled) return;
      tries += 1;
      let list: WebhookDelivery[] = [];
      try {
        list = await listWebhookDeliveries(app.name, selected, 20);
      } catch {
        /* transient: retry */
      }
      if (cancelled) return;
      if (list.length) setDeliveries(list);
      const row = list.find((d) => d.exec_id === pendingExecId);
      if ((row && row.outcome !== 'queued') || tries >= 15) {
        setPolling(false);
        return;
      }
      timer = setTimeout(() => void tick(), 1500);
    };
    timer = setTimeout(() => void tick(), 1200);
    return () => {
      cancelled = true;
      clearTimeout(timer);
    };
  }, [pendingExecId, app, selected]);

  const endpoint = app && selected ? `POST /api/v1/webhooks/${app.name}/${selected}` : '';

  const deliveryColumns: ColumnsType<WebhookDelivery> = [
    { title: 'Time', dataIndex: 'received_at', key: 'received_at', render: (v: string) => new Date(v).toLocaleString() },
    { title: 'Source', dataIndex: 'source', key: 'source', render: (v: string) => <Tag color={sourceColor(v)}>{v}</Tag> },
    { title: 'Outcome', dataIndex: 'outcome', key: 'outcome', render: (v: string) => <Tag color={outcomeColor(v)}>{v}</Tag> },
    { title: 'Actor', dataIndex: 'actor', key: 'actor', ellipsis: true },
  ];

  return (
    <Modal
      maskClosable={false}
      open={open}
      title={app ? `Webhook Debug: ${app.name}` : 'Webhook Debug'}
      onCancel={onClose}
      footer={null}
      width={820}
    >
      <Space direction="vertical" size="middle" style={{ width: '100%' }}>
        {webhooks.length > 1 && (
          <Select
            value={selected}
            onChange={setSelected}
            style={{ width: 280 }}
            options={webhooks.map((w) => ({ value: w, label: w }))}
          />
        )}
        {endpoint && (
          <Typography.Text type="secondary">
            Endpoint: <Typography.Text code>{endpoint}</Typography.Text>
          </Typography.Text>
        )}

        <div>
          <Typography.Text type="secondary">Test payload (JSON)</Typography.Text>
          <Input.TextArea
            value={payload}
            onChange={(e) => setPayload(e.target.value)}
            rows={8}
            style={{ fontFamily: 'monospace', marginTop: 4 }}
            placeholder='{"key": "value"}'
          />
        </div>

        <Space>
          <Switch checked={execute} onChange={setExecute} />
          <Typography.Text>Execute for real{execute ? ' (runs the recipe)' : ' (dry-run preview)'}</Typography.Text>
        </Space>

        {error && <Alert type="error" message={error} showIcon />}

        <Space>
          <Button type="primary" loading={sending} disabled={!selected} onClick={send}>
            {execute ? 'Send & Execute' : 'Send (dry-run)'}
          </Button>
          <Button onClick={() => void refreshDeliveries()}>Refresh deliveries</Button>
        </Space>

        {result && (
          <Descriptions
            size="small"
            column={1}
            bordered
            title="Result"
            items={[
              {
                key: 'outcome',
                label: 'Outcome',
                children: <Tag color={outcomeColor(result.outcome)}>{result.outcome}</Tag>,
              },
              { key: 'auth', label: 'Auth', children: result.auth_ok ? 'ok' : 'failed' },
              { key: 'actor', label: 'Actor', children: result.actor || '—' },
              { key: 'async', label: 'Mode', children: result.async ? 'async' : 'sync' },
              ...(result.idempotency_key ? [{ key: 'idem', label: 'Idempotency key', children: <Typography.Text code>{result.idempotency_key}</Typography.Text> }] : []),
              {
                key: 'extracted',
                label: 'Extracted env',
                children: Object.keys(result.extracted || {}).length
                  ? <pre style={{ margin: 0 }}>{Object.entries(result.extracted).map(([k, v]) => `${k}=${v}`).join('\n')}</pre>
                  : '—',
              },
              ...(result.results && result.results.length
                ? [{
                    key: 'results',
                    label: 'Host results',
                    children: (
                      <pre style={{ margin: 0, maxHeight: 200, overflow: 'auto' }}>{formatResults(result.results)}</pre>
                    ),
                  }]
                : []),
              ...(result.error ? [{ key: 'err', label: 'Error', children: <Typography.Text type="danger">{result.error}</Typography.Text> }] : []),
            ]}
          />
        )}

        {result?.outcome === 'queued' && !result.exec_id && (
          <Alert
            type="info"
            showIcon
            message="Queued — enable session recording (record_dir) to view async results in the deliveries below."
          />
        )}

        <div>
          <Space>
            <Typography.Text strong>Recent deliveries</Typography.Text>
            {polling && <Spin size="small" />}
          </Space>
          <Table
            style={{ marginTop: 8 }}
            size="small"
            rowKey="id"
            columns={deliveryColumns}
            dataSource={deliveries}
            pagination={false}
            locale={{ emptyText: 'No deliveries captured yet' }}
            expandable={{
              expandedRowRender: (d) => (
                <Space direction="vertical" style={{ width: '100%' }}>
                  {d.error && (
                    <div>
                      <Typography.Text type="secondary">Error</Typography.Text>
                      <pre style={{ margin: '4px 0 0', color: '#ff4d4f' }}>{d.error}</pre>
                    </div>
                  )}
                  {d.results && d.results.length > 0 && (
                    <div>
                      <Typography.Text type="secondary">Host results</Typography.Text>
                      <pre style={{ margin: '4px 0 0', maxHeight: 200, overflow: 'auto' }}>{formatResults(d.results)}</pre>
                    </div>
                  )}
                  {d.extracted && Object.keys(d.extracted).length > 0 && (
                    <div>
                      <Typography.Text type="secondary">Extracted env</Typography.Text>
                      <pre style={{ margin: '4px 0 0' }}>{Object.entries(d.extracted).map(([k, v]) => `${k}=${v}`).join('\n')}</pre>
                    </div>
                  )}
                  <div>
                    <Typography.Text type="secondary">Body</Typography.Text>
                    <pre style={{ margin: '4px 0 0', maxHeight: 200, overflow: 'auto' }}>{d.body}</pre>
                  </div>
                </Space>
              ),
            }}
          />
        </div>
      </Space>
    </Modal>
  );
}
