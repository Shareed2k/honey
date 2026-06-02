import { cleanup, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { ConfigProvider, theme } from 'antd';
import { FeedbackTab } from './FeedbackTab';

afterEach(cleanup);

describe('FeedbackTab', () => {
  it('renders initial state with title', () => {
    render(
      <ConfigProvider theme={{ algorithm: theme.darkAlgorithm }}>
        <FeedbackTab />
      </ConfigProvider>
    );
    expect(screen.getByText(/Logs Feedback Loop/i)).toBeTruthy();
  });
});
