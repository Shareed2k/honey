import { cleanup, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { ConfigProvider, theme } from 'antd';
import { ConfigTab } from './ConfigTab';

vi.mock('../RawYamlEditor', () => ({ RawYamlEditor: () => <div data-testid="yaml-editor" /> }));
vi.mock('../ConfigBackendsSection', () => ({ ConfigBackendsSection: () => <div /> }));
// ConfigTab imports apiGet/apiPut from '../api/core' and fetchConfigSchema
// from '../api/config' — these must be mocked at the exact specifiers the
// component imports, not a '../api' barrel (no such module exists), or the
// component's real fetch() call fires against a relative URL, which Node's
// fetch (no browser origin to resolve against) rejects asynchronously and
// leaks as an unhandled rejection into whichever test runs next.
vi.mock('../api/core', () => ({
  apiGet: vi.fn().mockResolvedValue({ ok: true, headers: { get: () => null }, text: async () => '' }),
  apiPut: vi.fn().mockResolvedValue({ ok: true }),
}));
vi.mock('../api/config', () => ({
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
