import { cleanup, render, screen } from '@testing-library/react';
import { afterEach, describe, it, expect } from 'vitest';
import { ConfigProvider, theme } from 'antd';
import { BackendsTab } from './BackendsTab';

const wrap = (ui: React.ReactElement) => (
  <ConfigProvider theme={{ algorithm: theme.darkAlgorithm }}>{ui}</ConfigProvider>
);

afterEach(cleanup);

describe('BackendsTab', () => {
  it('renders without crashing with empty backends', () => {
    render(wrap(<BackendsTab backends={[]} error={null} />));
    expect(screen.getByRole('table')).toBeTruthy();
  });

  it('shows error when provided', () => {
    render(wrap(<BackendsTab backends={[]} error="connection failed" />));
    expect(screen.getByText('connection failed')).toBeTruthy();
  });

  it('renders backend rows', () => {
    render(wrap(<BackendsTab backends={[{ kind: 's3', name: 'main', hint: 'us-east-1' }]} error={null} />));
    expect(screen.getByText('s3')).toBeTruthy();
    expect(screen.getByText('main')).toBeTruthy();
  });
});
