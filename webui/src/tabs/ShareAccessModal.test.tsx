import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { ConfigProvider, theme } from 'antd';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { ShareAccessModal } from './ShareAccessModal';
import type { HostRecord } from '../HostPicker';
import * as jitApi from '../api/jit';

// createGrant is the only network call this component makes; mock it so the
// test never hits a real fetch().
vi.mock('../api/jit', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../api/jit')>();
  return {
    ...actual,
    createGrant: vi.fn(),
  };
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

const record: HostRecord = { provider: 'ssh', name: 'op-terminal', primary_ip: '10.0.0.1' };

function renderModal(muxSession: string) {
  return render(
    <ConfigProvider theme={{ algorithm: theme.darkAlgorithm }}>
      <ShareAccessModal record={record} open onClose={vi.fn()} liveSession={{ muxSession }} />
    </ConfigProvider>,
  );
}

describe('ShareAccessModal', () => {
  // NEW-4 regression: TerminalContext.tsx (the real caller) passes
  // liveSession as a fresh `{ muxSession }` object literal on every render,
  // and it re-renders every 3s from its intercept-session poll. Before the
  // fix, the modal's reset effect depended on that object's IDENTITY, so a
  // parent re-render mid-flow wiped the just-created link/code even though
  // the muxSession value never changed.
  it('keeps a created live-share link after the parent re-renders with a new (same-value) liveSession object', async () => {
    vi.mocked(jitApi.createGrant).mockResolvedValue({
      id: 'jit_1',
      code: 'abc123',
      link_path: '/?access=abc123',
      status: 'approved',
      require_approval: false,
    });

    const { rerender } = renderModal('honey_abc123');

    fireEvent.click(screen.getByText('Create link'));
    expect(await screen.findByText('Copy this link now — the code is shown only once.')).toBeTruthy();

    // Simulate the parent's 3s poll re-render: liveSession keeps the SAME
    // muxSession value but is a brand-new object.
    rerender(
      <ConfigProvider theme={{ algorithm: theme.darkAlgorithm }}>
        <ShareAccessModal record={record} open onClose={vi.fn()} liveSession={{ muxSession: 'honey_abc123' }} />
      </ConfigProvider>,
    );

    expect(screen.getByText('Copy this link now — the code is shown only once.')).toBeTruthy();
    expect(jitApi.createGrant).toHaveBeenCalledTimes(1);
  });

  it('does reset when muxSession itself actually changes (a genuinely different live session)', async () => {
    vi.mocked(jitApi.createGrant).mockResolvedValue({
      id: 'jit_1',
      code: 'abc123',
      link_path: '/?access=abc123',
      status: 'approved',
      require_approval: false,
    });

    const { rerender } = renderModal('honey_abc123');
    fireEvent.click(screen.getByText('Create link'));
    expect(await screen.findByText('Copy this link now — the code is shown only once.')).toBeTruthy();

    rerender(
      <ConfigProvider theme={{ algorithm: theme.darkAlgorithm }}>
        <ShareAccessModal record={record} open onClose={vi.fn()} liveSession={{ muxSession: 'honey_different' }} />
      </ConfigProvider>,
    );

    expect(screen.queryByText('Copy this link now — the code is shown only once.')).toBeNull();
    expect(screen.getByText('Create link')).toBeTruthy();
  });
});
