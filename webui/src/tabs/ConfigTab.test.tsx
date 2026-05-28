import { cleanup, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { ConfigProvider, theme } from 'antd';
import { ConfigTab } from './ConfigTab';

vi.mock('../RawYamlEditor', () => ({ RawYamlEditor: () => <div data-testid="yaml-editor" /> }));
vi.mock('../ConfigBackendsSection', () => ({ ConfigBackendsSection: () => <div /> }));
vi.mock('../api', () => ({
  apiGet: vi.fn().mockResolvedValue({ ok: true, headers: { get: () => null }, text: async () => '' }),
  apiPut: vi.fn().mockResolvedValue({ ok: true }),
  fetchConfigSchema: vi.fn().mockResolvedValue({ ui_schema: null }),
}));

afterEach(cleanup);

describe('ConfigTab', () => {
  it('renders without crashing', async () => {
    render(
      <ConfigProvider theme={{ algorithm: theme.darkAlgorithm }}>
        <ConfigTab />
      </ConfigProvider>
    );
    await waitFor(() => expect(screen.getByTestId('yaml-editor')).toBeTruthy());
  });
});
