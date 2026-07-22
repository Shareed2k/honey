import { render, screen } from '@testing-library/react';
import { describe, it, expect, vi } from 'vitest';
import StudioWorkspace from '../StudioWorkspace';

vi.mock('../../contexts/AppContext', () => ({
  useAppContext: () => ({ meta: { version: '9.9.9' } }),
}));

describe('StudioWorkspace shell', () => {
  it('mounts dockview and a panel that reads app context', async () => {
    render(<StudioWorkspace />);
    // HelloPanel renders the version pulled from context inside a dockview panel.
    expect(await screen.findByText(/context-ok v9.9.9/)).toBeTruthy();
  });
});
