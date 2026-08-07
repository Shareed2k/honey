import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { ConfigProvider, theme } from 'antd';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import GitLoadModal from './GitLoadModal';
import { apiGet } from '../api/core';

// GitLoadModal imports apiGet from '../api/core', not a '../api' barrel (no
// such module exists) — mocking the wrong specifier leaves the component's
// real fetch() call intact, which rejects asynchronously against a relative
// URL in the test environment and leaks into whichever test runs next.
vi.mock('../api/core', () => ({
  apiGet: vi.fn(),
}));

const originalGetComputedStyle = window.getComputedStyle.bind(window);

beforeEach(() => {
  vi.spyOn(window, 'getComputedStyle').mockImplementation((elt) => originalGetComputedStyle(elt));

  vi.mocked(apiGet).mockResolvedValue({
    ok: true,
    json: async () => ({
      git_url: 'https://github.com/org/repo.git',
      git_branch: 'main',
      git_user: '',
      git_pass_configured: false,
      git_ssh_configured: false,
    }),
  } as unknown as Response);
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe('GitLoadModal', () => {
  it('renders modal fields correctly', async () => {
    render(
      <ConfigProvider theme={{ algorithm: theme.darkAlgorithm }}>
        <GitLoadModal visible onCancel={vi.fn()} onLoad={vi.fn()} />
      </ConfigProvider>
    );

    // Wait for the defaults to load and populate the fields
    await waitFor(() => {
      const urlInput = screen.getByLabelText('Git Repository Clone URL') as HTMLInputElement;
      expect(urlInput.value).toBe('https://github.com/org/repo.git');
    });

    const branchInput = screen.getByLabelText('Target Branch') as HTMLInputElement;
    expect(branchInput.value).toBe('main');

    const pathInput = screen.getByLabelText('Recipe filename/path in Repo') as HTMLInputElement;
    expect(pathInput.value).toBe('');
  });

  it('validates that path ends with .cue', async () => {
    render(
      <ConfigProvider theme={{ algorithm: theme.darkAlgorithm }}>
        <GitLoadModal visible onCancel={vi.fn()} onLoad={vi.fn()} />
      </ConfigProvider>
    );

    await waitFor(() => {
      expect(screen.getByLabelText('Recipe filename/path in Repo')).toBeTruthy();
    });

    const pathInput = screen.getByLabelText('Recipe filename/path in Repo') as HTMLInputElement;
    fireEvent.change(pathInput, { target: { value: 'myrecipe.json' } });

    const loadBtn = screen.getByRole('button', { name: 'Load Recipe' });
    fireEvent.click(loadBtn);

    expect(await screen.findByText('Recipe path must end with .cue')).toBeTruthy();
  });

  it('calls onLoad when submitted with valid path', async () => {
    const loadMock = vi.fn().mockResolvedValue(undefined);
    render(
      <ConfigProvider theme={{ algorithm: theme.darkAlgorithm }}>
        <GitLoadModal visible onCancel={vi.fn()} onLoad={loadMock} />
      </ConfigProvider>
    );

    await waitFor(() => {
      expect(screen.getByLabelText('Recipe filename/path in Repo')).toBeTruthy();
    });

    const pathInput = screen.getByLabelText('Recipe filename/path in Repo') as HTMLInputElement;
    fireEvent.change(pathInput, { target: { value: 'myrecipe.cue' } });

    const loadBtn = screen.getByRole('button', { name: 'Load Recipe' });
    fireEvent.click(loadBtn);

    await waitFor(() => {
      expect(loadMock).toHaveBeenCalledWith({
        gitUrl: 'https://github.com/org/repo.git',
        gitBranch: 'main',
        path: 'myrecipe.cue',
        gitUser: '',
        gitPass: '',
        gitSsh: '',
      });
    });
  });
});
