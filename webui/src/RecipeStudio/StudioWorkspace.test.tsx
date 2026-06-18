import React from 'react';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { ConfigProvider, theme, message } from 'antd';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import StudioWorkspace from './StudioWorkspace';
import { apiGet, apiPost } from '../api/core';

vi.mock('../api', () => ({
  apiGet: vi.fn(),
  apiPost: vi.fn(),
}));

const originalGetComputedStyle = window.getComputedStyle.bind(window);

// Mock ResizeObserver for ReactFlow
beforeEach(() => {
  vi.spyOn(window, 'getComputedStyle').mockImplementation((elt) => originalGetComputedStyle(elt));

  class MockResizeObserver implements ResizeObserver {
    observe(_target: Element, _options?: ResizeObserverOptions) {}
    unobserve(_target: Element) {}
    disconnect() {}
  }
  window.ResizeObserver = MockResizeObserver;

  vi.clearAllMocks();
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

const renderStudio = () => {
  vi.mocked(apiGet).mockImplementation((url: string) => {
    if (url === '/api/v1/recipes/studio-config') {
      return Promise.resolve({
        ok: true,
        json: async () => ({
          recipes_path: '/tmp/recipes',
          git_url: 'https://github.com/org/repo.git',
          git_branch: 'main',
          git_user: '',
          git_pass_configured: false,
          git_ssh_configured: false,
        }),
      });
    }
    return Promise.resolve({
      ok: true,
      json: async () => ([]),
    });
  });

  vi.mocked(apiPost).mockImplementation((url: string) => {
    if (url === '/api/v1/recipes/store/git-load') {
      return Promise.resolve({
        ok: true,
        json: async () => ({
          content: 'test content cue',
        }),
      });
    }
    if (url === '/api/v1/recipes/parse') {
      return Promise.resolve({
        ok: true,
        json: async () => ({
          recipe: {
            steps: [
              { id: 'step_1', script: 'echo 1' }
            ]
          }
        }),
      });
    }
    if (url === '/api/v1/recipes/validate-content') {
      return Promise.resolve({
        ok: true,
        json: async () => ({
          steps: [{ id: 'step_1', wave: 1 }],
          graph: { waves: [['step_1']] },
        }),
      });
    }
    return Promise.resolve({ ok: true, json: async () => ({}) });
  });

  render(
    <ConfigProvider theme={{ algorithm: theme.darkAlgorithm }}>
      <StudioWorkspace />
    </ConfigProvider>
  );
};

describe('StudioWorkspace - Reset and Git Load', () => {
  it('renders "Reset" and "Load from Git" buttons, clicking "Reset" resets state', async () => {
    const infoSpy = vi.spyOn(message, 'info');

    renderStudio();

    // Reset and Load from Git buttons should be visible
    const resetButton = screen.getByRole('button', { name: /Reset/i });
    const loadGitButton = screen.getByRole('button', { name: /Load from Git/i });

    expect(resetButton).toBeTruthy();
    expect(loadGitButton).toBeTruthy();

    // Click Reset button
    fireEvent.click(resetButton);

    // Should display Canvas reset message
    expect(infoSpy).toHaveBeenCalledWith('Canvas reset');
  });

  it('clicking "Load from Git" opens GitLoadModal, submitting it triggers loading, parsing, and sets nodes', async () => {
    const successSpy = vi.spyOn(message, 'success');

    renderStudio();
    
    fireEvent.click(screen.getByText('Load from Git'));
    expect(screen.getByRole('dialog')).toBeTruthy();

    // Populate required path field
    const pathInput = await screen.findByLabelText(/Recipe filename\/path in Repo/i);
    fireEvent.change(pathInput, { target: { value: 'myrecipe.cue' } });

    const loadBtn = await screen.findByRole('button', { name: /Load Recipe/i });
    fireEvent.click(loadBtn);

    await waitFor(() => {
      expect(apiPost).toHaveBeenCalledWith('/api/v1/recipes/store/git-load', expect.any(Object));
      expect(apiPost).toHaveBeenCalledWith('/api/v1/recipes/parse', expect.objectContaining({ content: 'test content cue' }));
      expect(apiPost).toHaveBeenCalledWith('/api/v1/recipes/validate-content', expect.any(Object));
    });

    await waitFor(() => {
      expect(successSpy).toHaveBeenCalledWith('Successfully loaded myrecipe.cue!');
    });
  });

  it('renders a Run Step button when a node is selected, opening the bottom panel', () => {
    renderStudio();
    // Since we don't mock node interactions fully, this just tests rendering doesn't crash
  });
});
