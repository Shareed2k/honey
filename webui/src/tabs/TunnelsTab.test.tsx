import { cleanup, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { ConfigProvider, theme } from 'antd';
import { TunnelsTab } from './TunnelsTab';

afterEach(cleanup);

describe('TunnelsTab', () => {
  it('renders empty state', () => {
    render(
      <ConfigProvider theme={{ algorithm: theme.darkAlgorithm }}>
        <TunnelsTab onNavigateToSearch={() => {}} />
      </ConfigProvider>
    );
    expect(screen.getByText(/No active tunnels/i)).toBeTruthy();
  });
});
