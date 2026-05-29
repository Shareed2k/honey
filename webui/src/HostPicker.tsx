import { useMemo, useState, type ReactNode } from 'react';
import { useEffect } from 'react';
import { Input, Space, Table, Typography } from 'antd';
import type { ColumnsType } from 'antd/es/table';

export type HostRecord = {
  provider: string;
  name: string;
  primary_ip: string;
  extra_ips?: string[];
  zone?: string;
  region?: string;
  meta?: Record<string, string>;
};

export function recordKey(rec: HostRecord): string {
  return `${rec.provider}\x1e${rec.name}\x1e${rec.primary_ip}`;
}

export function recordHaystack(rec: HostRecord): string {
  const parts = [rec.provider, rec.name, rec.primary_ip, rec.zone || '', rec.region || ''];
  if (rec.extra_ips?.length) {
    parts.push(rec.extra_ips.join(' '));
  }
  if (rec.meta) {
    for (const v of Object.values(rec.meta)) {
      parts.push(v);
    }
  }
  return parts.join(' ').toLowerCase();
}

type Props = {
  records: HostRecord[];
  selectedKeys: Record<string, boolean>;
  onToggleRow: (rec: HostRecord) => void;
  /** Notifies the parent of the currently visible (filtered) records. */
  onVisibleRecordsChange?: (visible: HostRecord[]) => void;
  /** Click handler for the row body (outside checkbox/action controls). */
  onRowClick?: (rec: HostRecord, event: React.MouseEvent<HTMLElement>) => void;
  /** Drop handler for per-row file drag-and-drop. */
  onRowDrop?: (rec: HostRecord, files: FileList | null) => void;
  /** Returns true if the row should render with the "open detail" highlight. */
  isRowHighlighted?: (rec: HostRecord) => boolean;
  /** Renders per-row action cell content (e.g. Terminal / Upload buttons). */
  renderRowActions?: (rec: HostRecord) => ReactNode;
  /** Controlled filter string. When provided, overrides internal state. */
  filter?: string;
  /** Notified on filter change. Required if `filter` is controlled. */
  onFilterChange?: (q: string) => void;
};

export function HostPicker(props: Props) {
  const {
    records,
    selectedKeys,
    onToggleRow,
    onVisibleRecordsChange,
    onRowClick,
    onRowDrop,
    isRowHighlighted,
    renderRowActions,
    filter: filterProp,
    onFilterChange,
  } = props;

  const [filterInternal, setFilterInternal] = useState('');
  const filter = filterProp !== undefined ? filterProp : filterInternal;
  const setFilter = (next: string) => {
    if (onFilterChange) {
      onFilterChange(next);
    }
    if (filterProp === undefined) {
      setFilterInternal(next);
    }
  };

  const displayRecords = useMemo(() => {
    const q = filter.trim().toLowerCase();
    if (!q) {
      return records;
    }
    return records.filter((rec) => recordHaystack(rec).includes(q));
  }, [records, filter]);

  useEffect(() => {
    onVisibleRecordsChange?.(displayRecords);
  }, [displayRecords, onVisibleRecordsChange]);

  return (
    <>
      <Space style={{ marginBottom: 8 }}>
        <Input
          placeholder="Filter results (provider, name, IP, zone, meta…)"
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
          allowClear
          style={{ width: 300 }}
        />
        <Typography.Text type="secondary">
          {displayRecords.length} result{displayRecords.length === 1 ? '' : 's'}
        </Typography.Text>
      </Space>

      <Table<HostRecord>
        dataSource={displayRecords}
        rowKey={recordKey}
        size="small"
        pagination={{
          pageSize: 25,
          showSizeChanger: true,
          pageSizeOptions: ['25', '50', '100'],
          showTotal: (total) => `${total} results`,
        }}
        rowSelection={{
          selectedRowKeys: Object.keys(selectedKeys).filter((k) => selectedKeys[k]),
          onSelect: (record) => onToggleRow(record),
          onSelectAll: (_selected, _rows, changeRows) => {
            changeRows.forEach((r) => onToggleRow(r));
          },
        }}
        onRow={(rec) => ({
          style: { cursor: onRowClick ? 'pointer' : undefined },
          className: isRowHighlighted?.(rec) ? 'host-row--highlighted' : undefined,
          onClick: (e) => {
            const el = e.target as HTMLElement;
            if (!el.closest('button, input, a, textarea, select, label')) {
              onRowClick?.(rec, e);
            }
          },
          onDragOver: (e) => e.preventDefault(),
          onDrop: onRowDrop
            ? (e) => {
                e.preventDefault();
                onRowDrop(rec, e.dataTransfer.files);
              }
            : undefined,
        })}
        columns={[
          { title: 'Provider', dataIndex: 'provider', key: 'provider' },
          { title: 'Name', dataIndex: 'name', key: 'name' },
          { title: 'IP', dataIndex: 'primary_ip', key: 'primary_ip' },
          { title: 'Zone', dataIndex: 'zone', key: 'zone', render: (v?: string) => v ?? '' },
          ...(renderRowActions
            ? ([
                {
                  title: 'Actions',
                  key: 'actions',
                  render: (_: unknown, rec: HostRecord) => renderRowActions(rec),
                },
              ] as ColumnsType<HostRecord>)
            : []),
        ]}
      />
    </>
  );
}
