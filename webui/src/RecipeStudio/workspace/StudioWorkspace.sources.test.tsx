import { cleanup, render, screen, fireEvent, waitFor, within } from '@testing-library/react';
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';

// Same rationale as StudioWorkspace.integration.test.tsx: apiGet needs a
// branch per real caller (schema fetch, recipe-store list, dockview
// workspace restore, and GitLoadModal's own studio-config prefill), and
// apiPutJson is stubbed so attachWorkspaceSync's debounced save never throws
// mid-test. apiPost is mocked per-test below (Library/Git-load both post to
// it) via importOriginal so unrelated exports (apiGet's real siblings,
// getToken, etc.) stay real.
vi.mock('../../api/core', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../api/core')>();
  return {
    ...actual,
    apiGet: vi.fn(async (path: string) => {
      if (path.includes('schema')) return { ok: true, json: async () => ({}) };
      if (path === '/api/v1/recipes/store') return { ok: true, json: async () => [] };
      if (path.includes('studio-config')) {
        return {
          ok: true,
          json: async () => ({
            git_url: 'https://example.com/repo.git',
            git_branch: 'main',
            git_user: '',
            git_pass_configured: false,
            git_ssh_configured: false,
          }),
        };
      }
      // /api/v1/studio/workspace restore — no saved workspace.
      return { ok: true, json: async () => null };
    }),
    apiPost: vi.fn(),
    apiPutJson: vi.fn(async () => ({ ok: true })),
  };
});

// generateRecipe/fetchLibraryRecipes are the two network calls the
// Generate/Library flows own directly; everything else this module exports
// (listStepKinds, stepSchemaForKind, fetchStoredRecipe, ...) is left real via
// importOriginal so panels that depend on them (GraphPanel/StepEditorPanel)
// keep working.
vi.mock('../../api/recipes', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../api/recipes')>();
  return {
    ...actual,
    generateRecipe: vi.fn(),
    fetchLibraryRecipes: vi.fn(),
  };
});

vi.mock('../../contexts/AppContext', () => ({
  useAppContext: () => ({ meta: { version: '1' } }),
}));

import StudioWorkspace from '../StudioWorkspace';
import { useWorkspaceStore } from './store';
import { HostSelectionProvider } from '../../contexts/HostSelectionContext';
import { apiPost } from '../../api/core';
import { generateRecipe, fetchLibraryRecipes } from '../../api/recipes';

beforeEach(() => {
  useWorkspaceStore.setState({ docs: {}, active: null, schema: null });
  vi.mocked(apiPost).mockReset();
  vi.mocked(generateRecipe).mockReset();
  vi.mocked(fetchLibraryRecipes).mockReset();
});

afterEach(cleanup);

function renderShell() {
  return render(
    <HostSelectionProvider>
      <StudioWorkspace />
    </HostSelectionProvider>,
  );
}

describe('StudioWorkspace — AI Generate flow', () => {
  it('submitting an intent calls generateRecipe and creates a doc via createDocFromRecipe', async () => {
    vi.mocked(generateRecipe).mockResolvedValue({
      recipe: { name: 'generated', type: 'graph', steps: [{ id: 'gen_step', command: 'echo hi' }], defaults: { x: 1 } },
      explanation: 'did the thing',
    });

    renderShell();

    // Non-anchored: the trigger button's accessible name includes its icon's
    // aria-label ahead of the visible "Generate" text (e.g. "fire Generate").
    fireEvent.click(await screen.findByRole('button', { name: /generate/i }));

    const dialog = await screen.findByRole('dialog');
    const textarea = within(dialog).getByPlaceholderText(/describe what you want to automate/i);
    fireEvent.change(textarea, { target: { value: 'restart nginx' } });
    fireEvent.click(within(dialog).getByRole('button', { name: /^generate$/i }));

    await waitFor(() => {
      expect(generateRecipe).toHaveBeenCalledWith('restart nginx', '');
    });

    await waitFor(() => {
      const docs = useWorkspaceStore.getState().docs;
      const created = Object.values(docs).find((d) => d.stepData.gen_step);
      expect(created).toBeTruthy();
      expect(created!.dirty).toBe(true);
      expect(created!.recipeDefaults).toEqual({ x: 1 });
    });

    // the new doc's graph panel is focused.
    await waitFor(() => {
      expect(document.querySelector('.react-flow')).toBeTruthy();
    });
  });
});

describe('StudioWorkspace — Library flow', () => {
  it('selecting a library recipe parses its content and creates a doc', async () => {
    vi.mocked(fetchLibraryRecipes).mockResolvedValue([
      {
        name: 'Cat1',
        recipes: [
          {
            name: 'lib-recipe',
            filename: 'lib-recipe.cue',
            description: 'a library recipe',
            content: 'CUE_LIBRARY_CONTENT',
            category: 'Cat1',
          },
        ],
      },
    ]);
    vi.mocked(apiPost).mockResolvedValueOnce({
      ok: true,
      json: async () => ({
        recipe: { name: 'lib-recipe', type: 'graph', steps: [{ id: 'lib_step', command: 'echo lib' }] },
      }),
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
    } as any);

    renderShell();

    // Non-anchored: the trigger button's accessible name includes its icon's
    // aria-label ahead of the visible "Library" text.
    fireEvent.click(await screen.findByRole('button', { name: /library/i }));

    const card = await screen.findByText('lib-recipe');
    fireEvent.click(card);

    await waitFor(() => {
      expect(apiPost).toHaveBeenCalledWith('/api/v1/recipes/parse', { content: 'CUE_LIBRARY_CONTENT' });
    });

    await waitFor(() => {
      const doc = useWorkspaceStore.getState().docs['lib-recipe'];
      expect(doc).toBeTruthy();
      expect(doc.stepData.lib_step).toBeTruthy();
      expect(doc.originalCue).toBe('CUE_LIBRARY_CONTENT');
      expect(doc.dirty).toBe(true);
    });

    await waitFor(() => {
      expect(document.querySelector('.react-flow')).toBeTruthy();
    });
  });
});

describe('StudioWorkspace — Git-load flow', () => {
  it('submitting the Git modal calls the git-load + parse endpoints and creates a doc', async () => {
    vi.mocked(apiPost).mockImplementation(async (path: string) => {
      if (path === '/api/v1/recipes/store/git-load') {
        return { ok: true, json: async () => ({ content: 'CUE_GIT_CONTENT' }) } as unknown as Response;
      }
      if (path === '/api/v1/recipes/parse') {
        return {
          ok: true,
          json: async () => ({
            recipe: { name: 'git-recipe', type: 'graph', steps: [{ id: 'git_step', command: 'echo git' }] },
          }),
        } as unknown as Response;
      }
      throw new Error(`unexpected apiPost path: ${path}`);
    });

    renderShell();

    fireEvent.click(await screen.findByRole('button', { name: /load from git/i }));

    const pathInput = await screen.findByLabelText('Recipe filename/path in Repo');
    fireEvent.change(pathInput, { target: { value: 'recipes/foo.cue' } });

    const loadBtn = screen.getByRole('button', { name: 'Load Recipe' });
    fireEvent.click(loadBtn);

    await waitFor(() => {
      expect(apiPost).toHaveBeenCalledWith(
        '/api/v1/recipes/store/git-load',
        expect.objectContaining({ path: 'recipes/foo.cue', git_url: 'https://example.com/repo.git', git_branch: 'main' }),
      );
    });
    await waitFor(() => {
      expect(apiPost).toHaveBeenCalledWith('/api/v1/recipes/parse', { content: 'CUE_GIT_CONTENT' });
    });

    await waitFor(() => {
      const doc = useWorkspaceStore.getState().docs['recipes/foo.cue'];
      expect(doc).toBeTruthy();
      expect(doc.stepData.git_step).toBeTruthy();
      expect(doc.originalCue).toBe('CUE_GIT_CONTENT');
      expect(doc.dirty).toBe(true);
    });

    await waitFor(() => {
      expect(document.querySelector('.react-flow')).toBeTruthy();
    });
  });
});
