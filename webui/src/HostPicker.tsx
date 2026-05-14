import { useEffect, useMemo, useState, type ReactNode } from 'react';

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
  onRowClick?: (rec: HostRecord, event: React.MouseEvent<HTMLTableRowElement>) => void;
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

const DEFAULT_PAGE_SIZE = 25;

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

  const [pageSize, setPageSize] = useState(DEFAULT_PAGE_SIZE);
  const [currentPage, setCurrentPage] = useState(1);

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

  const totalRows = displayRecords.length;
  const totalPages = Math.max(1, Math.ceil(totalRows / pageSize));
  const pageStart = (currentPage - 1) * pageSize;
  const pageEnd = pageStart + pageSize;
  const pagedRecords = useMemo(
    () => displayRecords.slice(pageStart, pageEnd),
    [displayRecords, pageStart, pageEnd],
  );
  const showingFrom = totalRows === 0 ? 0 : pageStart + 1;
  const showingTo = totalRows === 0 ? 0 : Math.min(pageEnd, totalRows);

  useEffect(() => {
    setCurrentPage(1);
  }, [filter, pageSize, records]);

  useEffect(() => {
    setCurrentPage((p) => Math.min(Math.max(1, p), totalPages));
  }, [totalPages]);

  return (
    <>
      <div style={{ marginBottom: '0.5rem' }}>
        <input
          placeholder="Filter results (provider, name, IP, zone, meta…)"
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
          style={{ width: 'min(100%, 420px)' }}
        />
        <div style={{ display: 'flex', flexWrap: 'wrap', gap: '0.6rem', alignItems: 'center', marginTop: '0.4rem' }}>
          <span style={{ fontSize: '0.8rem', opacity: 0.75 }}>
            Showing {showingFrom}-{showingTo} of {totalRows} (total results: {records.length})
          </span>
          <label style={{ fontSize: '0.8rem', opacity: 0.9 }}>
            Rows per page{' '}
            <select
              value={pageSize}
              onChange={(e) => setPageSize(Number(e.target.value))}
              style={{ marginLeft: 4 }}
            >
              <option value={25}>25</option>
              <option value={50}>50</option>
              <option value={100}>100</option>
            </select>
          </label>
          <button type="button" disabled={currentPage <= 1} onClick={() => setCurrentPage((p) => p - 1)}>
            Prev
          </button>
          <span style={{ fontSize: '0.8rem' }}>
            Page {currentPage} of {totalPages}
          </span>
          <button type="button" disabled={currentPage >= totalPages} onClick={() => setCurrentPage((p) => p + 1)}>
            Next
          </button>
        </div>
      </div>

      <div style={{ overflowX: 'auto' }} onDragOver={(e) => e.preventDefault()}>
        <table>
          <thead>
            <tr>
              <th style={{ width: 36 }}>Sel.</th>
              <th>Provider</th>
              <th>Name</th>
              <th>IP</th>
              <th>Zone</th>
              {renderRowActions ? <th>Actions</th> : null}
            </tr>
          </thead>
          <tbody>
            {pagedRecords.map((rec) => {
              const highlighted = isRowHighlighted ? isRowHighlighted(rec) : false;
              return (
                <tr
                  key={recordKey(rec)}
                  style={{
                    cursor: onRowClick ? 'pointer' : undefined,
                    background: highlighted ? 'rgba(100, 149, 237, 0.12)' : undefined,
                  }}
                  onClick={
                    onRowClick
                      ? (e) => {
                          const el = e.target as HTMLElement;
                          if (el.closest('button, input, a, textarea, select, label')) {
                            return;
                          }
                          onRowClick(rec, e);
                        }
                      : undefined
                  }
                  onDragOver={(e) => e.preventDefault()}
                  onDrop={
                    onRowDrop
                      ? (e) => {
                          e.preventDefault();
                          onRowDrop(rec, e.dataTransfer.files);
                        }
                      : undefined
                  }
                >
                  <td>
                    <input
                      type="checkbox"
                      checked={!!selectedKeys[recordKey(rec)]}
                      onChange={() => onToggleRow(rec)}
                      aria-label={`Select ${rec.name}`}
                    />
                  </td>
                  <td>{rec.provider}</td>
                  <td>{rec.name}</td>
                  <td>{rec.primary_ip}</td>
                  <td>{rec.zone || ''}</td>
                  {renderRowActions ? (
                    <td style={{ whiteSpace: 'nowrap' }}>{renderRowActions(rec)}</td>
                  ) : null}
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
    </>
  );
}
