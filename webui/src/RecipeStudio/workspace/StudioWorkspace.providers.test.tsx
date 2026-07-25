import { cleanup, render, screen, waitFor } from '@testing-library/react';
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { ConfigProvider, theme } from 'antd';

// Real provider chain (contexts/index.tsx's RootProvider — the same wrapper
// main.tsx puts around <App/>, and therefore around the `studio` tab's
// <StudioWorkspace/>). Unlike StudioWorkspace.integration.test.tsx (which
// stubs out useAppContext entirely), this test exercises every provider's
// real mount-time effects (AppProvider's meta/backends fetch,
// HostSelectionProvider's providers fetch, StudioWorkspace's own
// schema/store fetches, and persistence.ts's workspace restore) — the only
// thing mocked is the network boundary itself, `api/core`'s apiGet/
// apiPutJson, via importOriginal so every other export (getToken, apiPost,
// etc.) stays real.
vi.mock('../../api/core', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../api/core')>();
  return {
    ...actual,
    apiGet: vi.fn(async (path: string) => ({
      ok: true,
      json: async () => {
        if (path.includes('/studio/workspace')) return null; // no saved workspace — onReady's default layout stands
        if (path.includes('/recipes/schema')) return {};
        if (path.includes('/recipes/store')) return [];
        if (path.includes('/backends')) return { backends: [] };
        if (path.includes('/providers')) return { providers: [] };
        if (path.includes('/meta')) return { version: 'test', config_path: '/tmp/config.yaml' };
        return {};
      },
    })),
    apiPutJson: vi.fn(async () => ({ ok: true })),
  };
});

import StudioWorkspace from '../StudioWorkspace';
import { RootProvider } from '../../contexts';
import { useWorkspaceStore } from './store';

beforeEach(() => {
  useWorkspaceStore.setState({ docs: {}, active: null, schema: null });
});

afterEach(cleanup);

function renderUnderRealProviders() {
  return render(
    <ConfigProvider theme={{ algorithm: theme.darkAlgorithm }}>
      <RootProvider>
        <StudioWorkspace />
      </RootProvider>
    </ConfigProvider>,
  );
}

describe('StudioWorkspace under the real provider chain', () => {
  it('mounts without throwing and renders the dockview shell', async () => {
    expect(() => renderUnderRealProviders()).not.toThrow();

    // Top-bar "New Recipe" control (part of StudioWorkspace's own chrome, not
    // any mocked component) proves the shell rendered past every provider's
    // mount-time effects (AppProvider/HostSelectionProvider's fetches,
    // persistence.ts's workspace restore) without an unhandled throw/rejection
    // bubbling up.
    expect(await screen.findByRole('button', { name: /new recipe/i })).toBeTruthy();

    // The default tool-panel layout (Toolbox/Records/Step/Run) is applied by
    // `onReady` — its presence confirms dockview itself mounted and laid out
    // real panels (not just the shell's own non-dockview chrome).
    await waitFor(() => {
      expect(screen.getByText('Toolbox')).toBeTruthy();
    });
  });
});
