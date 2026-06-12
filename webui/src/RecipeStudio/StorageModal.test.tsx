import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { ConfigProvider, theme } from 'antd';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import StorageModal from './StorageModal';

beforeEach(() => {
  const getComputedStyle = window.getComputedStyle.bind(window);
  vi.spyOn(window, 'getComputedStyle').mockImplementation((elt) => getComputedStyle(elt));
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe('StorageModal', () => {
  it('only shows commit message for Git saves', () => {
    render(
      <ConfigProvider theme={{ algorithm: theme.darkAlgorithm }}>
        <StorageModal
          visible
          currentRecipeName="ops.cue"
          onCancel={vi.fn()}
          onSave={vi.fn()}
        />
      </ConfigProvider>
    );

    expect(screen.queryByText('Save / Commit Message')).toBeNull();

    fireEvent.click(screen.getByText('Git Repository'));

    expect(screen.getByText('Save / Commit Message')).toBeTruthy();
  });

  it('defaults new recipes to cue and rejects non-cue filenames', async () => {
    const onSaveMock = vi.fn();
    render(
      <ConfigProvider theme={{ algorithm: theme.darkAlgorithm }}>
        <StorageModal
          visible
          onCancel={vi.fn()}
          onSave={onSaveMock}
        />
      </ConfigProvider>
    );

    const input = screen.getByLabelText('Recipe filename') as HTMLInputElement;
    expect(input.value).toBe('visual-studio-recipe.cue');

    fireEvent.change(input, { target: { value: 'bad.json' } });
    fireEvent.click(screen.getByText('Save Recipe'));

    expect(await screen.findByText('Recipe filename must end with .cue')).toBeTruthy();
    expect(onSaveMock).not.toHaveBeenCalled();
  });
});
