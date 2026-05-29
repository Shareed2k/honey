import { cleanup, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { ApiDocsTab } from './ApiDocsTab';

vi.mock('../OpenApiDocsTab', () => ({ OpenApiDocsTab: () => <div data-testid="swagger" /> }));

afterEach(cleanup);

describe('ApiDocsTab', () => {
  it('renders without crashing', async () => {
    render(<ApiDocsTab />);
    expect(await screen.findByTestId('swagger')).toBeTruthy();
  });
});
