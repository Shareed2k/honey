import { Alert, Table } from 'antd';
import type { ColumnsType } from 'antd/es/table';

type BackendRow = { kind: string; name: string; hint: string };

interface Props {
  backends: BackendRow[];
  error: string | null;
}

const columns: ColumnsType<BackendRow> = [
  { title: 'Kind', dataIndex: 'kind', key: 'kind', width: 120 },
  { title: 'Name', dataIndex: 'name', key: 'name', width: 200 },
  { title: 'Hint', dataIndex: 'hint', key: 'hint' },
];

export function BackendsTab({ backends, error }: Props) {
  return (
    <>
      {error && <Alert type="error" message={error} style={{ marginBottom: 12 }} />}
      <Table
        dataSource={backends}
        columns={columns}
        rowKey={(r) => `${r.kind}-${r.name}`}
        size="small"
        pagination={false}
      />
    </>
  );
}
