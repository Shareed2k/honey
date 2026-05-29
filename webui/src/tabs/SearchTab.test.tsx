import { cleanup, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { ConfigProvider, theme } from 'antd';
import { SearchTab } from './SearchTab';

vi.mock('../HostPicker', () => ({
  HostPicker: () => <div data-testid="host-picker" />,
  recordKey: (r: { provider: string; name: string }) => `${r.provider}:${r.name}`,
  recordHaystack: () => '',
}));

afterEach(cleanup);

describe('SearchTab', () => {
  it('renders without crashing', () => {
    render(
      <ConfigProvider theme={{ algorithm: theme.darkAlgorithm }}>
        <SearchTab
          records={[]}
          selectedKeys={{}}
          onRecordsChange={() => {}}
          onSelectedKeysChange={() => {}}
          selectedProviders={[]}
          onSelectedProvidersChange={() => {}}
          selectedBackends={[]}
          onSelectedBackendsChange={() => {}}
          backends={[]}
          providerIds={[]}
          sshUser=""
          onSshUserChange={() => {}}
          meta={null}
          onOpenTunnel={() => {}}
          onOpenReplay={() => {}}
          onOpenReplayAll={() => {}}
          onOpenTerminal={() => {}}
        />
      </ConfigProvider>
    );
    expect(screen.getByTestId('host-picker')).toBeTruthy();
  });
});
