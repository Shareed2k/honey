import { cleanup, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { StepRetryEditor } from './StepEditors';

describe('StepRetryEditor', () => {
  afterEach(() => cleanup());

  it('enabling sets default retry', async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(<StepRetryEditor retry={undefined} onChange={onChange} />);
    await user.click(screen.getByRole('checkbox', { name: 'retry' }));
    expect(onChange).toHaveBeenCalledWith(
      expect.objectContaining({ attempts: 3, backoff: 'fixed' }),
    );
  });

  it('disabling clears retry', async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(
      <StepRetryEditor
        retry={{ attempts: 3, delay_ms: 1000, max_delay_ms: 30000, backoff: 'fixed' }}
        onChange={onChange}
      />,
    );
    await user.click(screen.getByRole('checkbox', { name: 'retry' }));
    expect(onChange).toHaveBeenCalledWith(undefined);
  });
});
