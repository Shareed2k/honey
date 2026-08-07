import { cleanup, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { ConfigProvider, theme } from 'antd';
import { FilesTab } from './FilesTab';
import { RootProvider } from '../contexts';

// RootProvider's nested contexts (AppContext, HostSelectionContext,
// RecipeAssistContext) call apiGet on mount — mock it so those calls never
// hit real network (a relative-URL fetch() rejects in the test environment,
// same class of bug fixed elsewhere for the stale '../api' mock path).
// Partial mock (importOriginal): AppContext also needs getToken/apiHeaders
// from this same module — replacing the whole module would leave those
// undefined and crash AppContext's own mount effect.
vi.mock('../api/core', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../api/core')>();
  return {
    ...actual,
    apiGet: vi.fn().mockResolvedValue({ ok: true, json: async () => ({}) }),
  };
});

afterEach(cleanup);

describe('FilesTab', () => {
  it('renders without crashing', () => {
    render(
      <ConfigProvider theme={{ algorithm: theme.darkAlgorithm }}>
        <RootProvider>
          <FilesTab />
        </RootProvider>
      </ConfigProvider>
    );
    expect(screen.getAllByText(/Source host/i).length).toBeGreaterThan(0);
  });
});
