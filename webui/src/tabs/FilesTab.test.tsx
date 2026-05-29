import { cleanup, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { ConfigProvider, theme } from 'antd';
import { FilesTab } from './FilesTab';

afterEach(cleanup);

describe('FilesTab', () => {
  it('renders without crashing', () => {
    render(
      <ConfigProvider theme={{ algorithm: theme.darkAlgorithm }}>
        <FilesTab records={[]} backends={[]} />
      </ConfigProvider>
    );
    expect(screen.getAllByText(/Source host/i).length).toBeGreaterThan(0);
  });
});
