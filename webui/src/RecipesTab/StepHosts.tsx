import { useMemo, useState } from 'react';
import { HostPicker, recordKey, type HostRecord } from '../HostPicker';

type Props = {
  records: HostRecord[];
  hosts: HostRecord[];
  onHostsChange: (h: HostRecord[]) => void;
  onNext: () => void;
  reconcileNote?: string | null;
};

export function StepHosts({ records, hosts, onHostsChange, onNext, reconcileNote }: Props) {
  const [filter, setFilter] = useState('');

  const selectedKeys = useMemo(() => {
    const map: Record<string, boolean> = {};
    for (const h of hosts) {
      map[recordKey(h)] = true;
    }
    return map;
  }, [hosts]);

  const toggleRow = (rec: HostRecord) => {
    const key = recordKey(rec);
    if (selectedKeys[key]) {
      onHostsChange(hosts.filter((h) => recordKey(h) !== key));
    } else {
      onHostsChange([...hosts, rec]);
    }
  };

  return (
    <div className="rcp-step rcp-step--hosts">
      <header className="rcp-step__header">
        <h2>① Pick hosts</h2>
        <p className="rcp-step__hint">
          {hosts.length === 0
            ? 'Select hosts to run this recipe against. Selections sync with the Search tab.'
            : `${hosts.length} host${hosts.length === 1 ? '' : 's'} pre-filled from Search.`}
        </p>
        {reconcileNote ? <p className="rcp-warn">{reconcileNote}</p> : null}
      </header>
      <HostPicker
        records={records}
        selectedKeys={selectedKeys}
        onToggleRow={toggleRow}
        filter={filter}
        onFilterChange={setFilter}
      />
      <footer className="rcp-step__footer">
        <button
          type="button"
          className="rcp-btn rcp-btn--pri"
          disabled={hosts.length === 0}
          onClick={onNext}
        >
          Next → recipe
        </button>
      </footer>
    </div>
  );
}
