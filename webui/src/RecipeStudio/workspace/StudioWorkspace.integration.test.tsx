import { cleanup, render, screen, fireEvent, waitFor } from '@testing-library/react';
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';

vi.mock('../../api/core', () => ({
  apiGet: vi.fn(async (path: string) => ({
    ok: true,
    json: async () => (path.includes('schema') ? {} : []),
  })),
}));
vi.mock('../../contexts/AppContext', () => ({
  useAppContext: () => ({ meta: { version: '1' } }),
}));

import StudioWorkspace from '../StudioWorkspace';
import { useWorkspaceStore } from './store';

beforeEach(() => {
  useWorkspaceStore.setState({ docs: {}, active: null, schema: null });
});

afterEach(cleanup);

describe('StudioWorkspace shell', () => {
  it('opening a recipe adds a graph panel and sets it active', async () => {
    render(<StudioWorkspace />);

    fireEvent.click(await screen.findByRole('button', { name: /new recipe/i }));

    await waitFor(() => {
      const ids = Object.keys(useWorkspaceStore.getState().docs);
      expect(ids.some((id) => id.startsWith('untitled-'))).toBe(true);
    });

    await waitFor(() => {
      expect(document.querySelector('.react-flow')).toBeTruthy();
    });
  });
});
