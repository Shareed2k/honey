import { Suspense, lazy } from 'react';
import { Spin } from 'antd';

const OpenApiDocsTab = lazy(() =>
  import('../OpenApiDocsTab').then((m) => ({ default: m.OpenApiDocsTab }))
);

export function ApiDocsTab() {
  return (
    <Suspense fallback={<Spin tip="Loading API explorer…" style={{ display: 'block', marginTop: 32 }} />}>
      <OpenApiDocsTab />
    </Suspense>
  );
}
