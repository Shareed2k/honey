import { useCallback, useEffect, useState } from 'react';
import {
  Alert,
  Button,
  Input,
  InputNumber,
  Popconfirm,
  Select,
  Space,
  Switch,
  Table,
  Typography,
  message,
} from 'antd';
import { BulbOutlined, DeleteOutlined, ReloadOutlined, SaveOutlined } from '@ant-design/icons';
import type { ColumnsType } from 'antd/es/table';
import { fetchFeedbackRecords, saveFeedbackRecords, suggestFeedbackAnomaly } from '../api';
import type { FeedbackRecord } from '../api';

export function FeedbackTab() {
  const [records, setRecords] = useState<FeedbackRecord[]>([]);
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [searchText, setSearchText] = useState('');
  const [typeFilter, setTypeFilter] = useState<'all' | 'anomaly' | 'normal'>('all');
  const [aiLoading, setAiLoading] = useState<Record<string, boolean>>({});

  const loadRecords = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const data = await fetchFeedbackRecords();
      setRecords(data);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void loadRecords();
  }, [loadRecords]);

  const handleSave = async () => {
    setSaving(true);
    setError(null);
    try {
      await saveFeedbackRecords(records);
      void message.success('Changes saved successfully!');
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      void message.error(`Failed to save changes: ${msg}`);
      setError(`Save error: ${msg}`);
    } finally {
      setSaving(false);
    }
  };

  const handleAnomalyToggle = (record: FeedbackRecord, checked: boolean) => {
    const idx = records.indexOf(record);
    if (idx !== -1) {
      const updated = [...records];
      updated[idx] = { ...record, anomaly: checked };
      setRecords(updated);
    }
  };

  const handleScoreChange = (record: FeedbackRecord, value: number | null) => {
    const idx = records.indexOf(record);
    if (idx !== -1) {
      const updated = [...records];
      updated[idx] = { ...record, score: value === null ? 0 : value };
      setRecords(updated);
    }
  };

  const handleReasonChange = (record: FeedbackRecord, value: string) => {
    const idx = records.indexOf(record);
    if (idx !== -1) {
      const updated = [...records];
      updated[idx] = { ...record, reason: value };
      setRecords(updated);
    }
  };

  const handleDelete = (record: FeedbackRecord) => {
    const idx = records.indexOf(record);
    if (idx !== -1) {
      const updated = [...records];
      updated.splice(idx, 1);
      setRecords(updated);
    }
  };

  const handleAiSuggest = async (record: FeedbackRecord) => {
    const rowKey = `${record.ts}-${record.line}`;
    setAiLoading((prev) => ({ ...prev, [rowKey]: true }));
    try {
      const res = await suggestFeedbackAnomaly(record.line, record.source);
      const idx = records.indexOf(record);
      if (idx !== -1) {
        const updated = [...records];
        updated[idx] = {
          ...record,
          anomaly: res.anomaly,
          score: res.score,
          reason: res.reason,
        };
        setRecords(updated);
      }
      void message.success('AI suggestion successfully applied!');
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      void message.error(`Failed to get AI suggestion: ${msg}`);
    } finally {
      setAiLoading((prev) => ({ ...prev, [rowKey]: false }));
    }
  };

  const filteredRecords = records.filter((rec) => {
    if (typeFilter === 'anomaly' && !rec.anomaly) {
      return false;
    }
    if (typeFilter === 'normal' && rec.anomaly) {
      return false;
    }
    if (searchText.trim()) {
      const query = searchText.toLowerCase();
      const lineMatch = (rec.line || '').toLowerCase().includes(query);
      const reasonMatch = (rec.reason || '').toLowerCase().includes(query);
      const sourceMatch = (rec.source || '').toLowerCase().includes(query);
      return lineMatch || reasonMatch || sourceMatch;
    }
    return true;
  });

  const columns: ColumnsType<FeedbackRecord> = [
    {
      title: 'Timestamp',
      dataIndex: 'ts',
      key: 'ts',
      width: 170,
      render: (ts: string) => (
        <span style={{ fontFamily: 'monospace', fontSize: '0.85rem' }}>{ts}</span>
      ),
    },
    {
      title: 'Source',
      dataIndex: 'source',
      key: 'source',
      width: 120,
      render: (source: string) => (
        <span style={{ fontFamily: 'monospace', fontSize: '0.85rem' }}>{source}</span>
      ),
    },
    {
      title: 'Status',
      dataIndex: 'anomaly',
      key: 'anomaly',
      width: 140,
      render: (anomaly: boolean, record) => (
        <Switch
          checked={anomaly}
          checkedChildren="Anomaly"
          unCheckedChildren="Normal"
          onChange={(checked) => handleAnomalyToggle(record, checked)}
        />
      ),
    },
    {
      title: 'Log Line / Template',
      dataIndex: 'line',
      key: 'line',
      render: (line: string) => (
        <pre
          style={{
            margin: 0,
            padding: '4px 8px',
            background: '#fafafa',
            border: '1px solid #e8e8e8',
            borderRadius: 4,
            maxHeight: 100,
            overflowY: 'auto',
            fontFamily: 'monospace',
            fontSize: '0.8rem',
            whiteSpace: 'pre-wrap',
            wordBreak: 'break-all',
          }}
        >
          <code>{line}</code>
        </pre>
      ),
    },
    {
      title: 'Score',
      dataIndex: 'score',
      key: 'score',
      width: 100,
      render: (score: number, record) => (
        <InputNumber
          min={0}
          max={1}
          step={0.01}
          value={score}
          onChange={(val) => handleScoreChange(record, val)}
          style={{ width: '100%' }}
        />
      ),
    },
    {
      title: 'Reason',
      dataIndex: 'reason',
      key: 'reason',
      width: 250,
      render: (reason: string, record) => (
        <Input
          value={reason}
          onChange={(e) => handleReasonChange(record, e.target.value)}
          placeholder="Reason for classification"
        />
      ),
    },
    {
      title: 'Actions',
      key: 'actions',
      align: 'right',
      width: 220,
      render: (_, record) => {
        const rowKey = `${record.ts}-${record.line}`;
        return (
          <Space>
            <Button
              size="small"
              icon={<BulbOutlined />}
              onClick={() => void handleAiSuggest(record)}
              loading={aiLoading[rowKey]}
            >
              AI Suggest
            </Button>
            <Popconfirm
              title="Delete feedback entry?"
              description="Are you sure you want to remove this log line feedback entry?"
              onConfirm={() => handleDelete(record)}
              okText="Yes"
              cancelText="No"
            >
              <Button size="small" danger icon={<DeleteOutlined />}>
                Delete
              </Button>
            </Popconfirm>
          </Space>
        );
      },
    },
  ];

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
      {error && <Alert type="error" message={error} banner closable onClose={() => setError(null)} />}

      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', flexWrap: 'wrap', gap: 12 }}>
        <Space size="middle">
          <Typography.Title level={4} style={{ margin: 0 }}>
            Logs Feedback Loop
          </Typography.Title>
          <Button icon={<ReloadOutlined />} onClick={() => void loadRecords()} loading={loading}>
            Reload
          </Button>
          <Button type="primary" icon={<SaveOutlined />} onClick={() => void handleSave()} loading={saving}>
            Save Changes
          </Button>
        </Space>

        <Space>
          <Input
            placeholder="Search line, source, or reason..."
            value={searchText}
            onChange={(e) => setSearchText(e.target.value)}
            style={{ width: 280 }}
            allowClear
          />
          <Select
            value={typeFilter}
            onChange={setTypeFilter}
            style={{ width: 160 }}
            options={[
              { value: 'all', label: 'Show All' },
              { value: 'anomaly', label: 'Show Anomalies' },
              { value: 'normal', label: 'Show Normal' },
            ]}
          />
        </Space>
      </div>

      <Table
        dataSource={filteredRecords}
        columns={columns}
        rowKey={(record) => `${record.ts}-${record.line}`}
        loading={loading}
        size="small"
        pagination={{ pageSize: 25 }}
      />
    </div>
  );
}
