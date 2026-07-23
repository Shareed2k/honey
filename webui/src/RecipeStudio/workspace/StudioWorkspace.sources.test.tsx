import { cleanup, render, screen, fireEvent, waitFor, within } from '@testing-library/react';
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';

// Default apiGet behavior, hoisted so both the vi.mock factory below and
// individual tests (to restore the default after overriding it for a
// collision scenario) can reference the same implementation.
const { defaultApiGet } = vi.hoisted(() => {
  return {
    defaultApiGet: async (path: string): Promise<Response> => {
      if (path.includes('schema')) return { ok: true, json: async () => ({}) } as unknown as Response;
      // Store listing (fetchRecipeStoreList / the Open dropdown's own fetch) —
      // empty by default; collision tests below override this to simulate an
      // already-saved name.
      if (path === '/api/v1/recipes/store') {
        return { ok: true, json: async () => [] } as unknown as Response;
      }
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
        } as unknown as Response;
      }
      // GET /api/v1/recipes/store/{name} (fetchStoredRecipe's StoreLoadResponse
      // envelope) — createDoc(name) reads data.recipe/data.raw_cue directly, so
      // this is what proves the Library/Git-load "import then open" flow opens
      // via the SAME store-backed load Open/Generate use, not a client-side parse.
      return {
        ok: true,
        json: async () => ({
          recipe: { name: 'stored', type: 'graph', steps: [{ id: 'stored_step', command: 'echo stored' }] },
          raw_cue: 'STORED_CUE',
        }),
      } as unknown as Response;
    },
  };
});

// apiGet is mocked here (with a resettable default, see above) since both the
// Library and Git-load flows now hit it (store list + store-by-name load), not
// just the schema/store-list fetches on mount. apiPost is mocked per-test
// below (both flows now POST a SAVE to /api/v1/recipes/store/{name}, and
// Git-load also POSTs to /api/v1/recipes/store/git-load) via importOriginal so
// unrelated exports (getToken, etc.) stay real.
vi.mock('../../api/core', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../api/core')>();
  return {
    ...actual,
    apiGet: vi.fn(defaultApiGet),
    apiPost: vi.fn(),
    apiPutJson: vi.fn(async () => ({ ok: true })),
  };
});

// generateRecipe/fetchLibraryRecipes are the two network calls the
// Generate/Library flows own directly; everything else this module exports
// (listStepKinds, stepSchemaForKind, fetchStoredRecipe, saveStoredRecipe,
// fetchRecipeStoreList, ...) is left real via importOriginal so the Library/
// Git-load "import into the store" flows exercise their REAL implementations
// (which call through to the mocked api/core apiGet/apiPost above).
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
import { apiGet, apiPost } from '../../api/core';
import { generateRecipe, fetchLibraryRecipes } from '../../api/recipes';

beforeEach(() => {
  useWorkspaceStore.setState({ docs: {}, active: null, schema: null });
  vi.mocked(apiGet).mockReset();
  vi.mocked(apiGet).mockImplementation(defaultApiGet);
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

describe('StudioWorkspace — Library flow (import into the store, then open)', () => {
  const libCategories = [
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
  ];

  it('selecting a recipe SAVES its CUE content to the store, then opens it via createDoc', async () => {
    vi.mocked(fetchLibraryRecipes).mockResolvedValue(libCategories);
    vi.mocked(apiPost).mockResolvedValue({ ok: true } as Response);

    renderShell();

    // Non-anchored: the trigger button's accessible name includes its icon's
    // aria-label ahead of the visible "Library" text.
    fireEvent.click(await screen.findByRole('button', { name: /library/i }));

    const card = await screen.findByText('lib-recipe');
    fireEvent.click(card);

    // SAVE: the library entry's own filename is used as the store name, and
    // its raw CUE content is posted verbatim (not re-parsed client-side).
    await waitFor(() => {
      expect(apiPost).toHaveBeenCalledWith('/api/v1/recipes/store/lib-recipe.cue', { content: 'CUE_LIBRARY_CONTENT' });
    });

    // LOAD: createDoc('lib-recipe.cue') opens it through the SAME store-backed
    // load Open/Generate use (GET /api/v1/recipes/store/{name}) — the doc's
    // stepData/originalCue below come from the STORED envelope mocked in
    // defaultApiGet above, proving it went through createDoc rather than a
    // client-side parse of the raw content.
    await waitFor(() => {
      const doc = useWorkspaceStore.getState().docs['lib-recipe.cue'];
      expect(doc).toBeTruthy();
      expect(doc.stepData.stored_step).toBeTruthy();
      expect(doc.originalCue).toBe('STORED_CUE');
    });

    await waitFor(() => {
      expect(document.querySelector('.react-flow')).toBeTruthy();
    });
  });

  it('a name collision with an existing stored recipe is suffixed, never clobbered', async () => {
    vi.mocked(fetchLibraryRecipes).mockResolvedValue(libCategories);
    vi.mocked(apiPost).mockResolvedValue({ ok: true } as Response);
    // The store already has a recipe saved under this exact name.
    vi.mocked(apiGet).mockImplementation(async (path: string) => {
      if (path === '/api/v1/recipes/store') {
        return {
          ok: true,
          json: async () => [{ name: 'lib-recipe.cue', path: '/recipes/lib-recipe.cue' }],
        } as unknown as Response;
      }
      return defaultApiGet(path);
    });

    renderShell();

    fireEvent.click(await screen.findByRole('button', { name: /library/i }));
    const card = await screen.findByText('lib-recipe');
    fireEvent.click(card);

    await waitFor(() => {
      expect(apiPost).toHaveBeenCalledWith('/api/v1/recipes/store/lib-recipe-2.cue', { content: 'CUE_LIBRARY_CONTENT' });
    });
    // The collision name must never be saved to (that would silently clobber
    // the already-stored recipe).
    expect(apiPost).not.toHaveBeenCalledWith('/api/v1/recipes/store/lib-recipe.cue', expect.anything());

    await waitFor(() => {
      expect(useWorkspaceStore.getState().docs['lib-recipe-2.cue']).toBeTruthy();
    });
    expect(useWorkspaceStore.getState().docs['lib-recipe.cue']).toBeFalsy();
  });
});

describe('StudioWorkspace — Git-load flow (import into the store, then open)', () => {
  function mockGitLoadPost(extra?: (path: string) => Response | undefined) {
    vi.mocked(apiPost).mockImplementation(async (path: string) => {
      if (path === '/api/v1/recipes/store/git-load') {
        return { ok: true, json: async () => ({ content: 'CUE_GIT_CONTENT' }) } as unknown as Response;
      }
      const overridden = extra?.(path);
      if (overridden) return overridden;
      if (path.startsWith('/api/v1/recipes/store/')) {
        return { ok: true } as unknown as Response;
      }
      throw new Error(`unexpected apiPost path: ${path}`);
    });
  }

  it('git-load fetches content, SAVES it to the store under the path basename, then opens it via createDoc', async () => {
    mockGitLoadPost();

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
    // SAVE: keyed by the git path's basename ("foo.cue"), not the full repo path.
    await waitFor(() => {
      expect(apiPost).toHaveBeenCalledWith('/api/v1/recipes/store/foo.cue', { content: 'CUE_GIT_CONTENT' });
    });

    // LOAD: createDoc('foo.cue') opens it from the store — the STORED envelope
    // (mocked in defaultApiGet) proves this, not a client-side parse of content.
    await waitFor(() => {
      const doc = useWorkspaceStore.getState().docs['foo.cue'];
      expect(doc).toBeTruthy();
      expect(doc.stepData.stored_step).toBeTruthy();
      expect(doc.originalCue).toBe('STORED_CUE');
    });

    await waitFor(() => {
      expect(document.querySelector('.react-flow')).toBeTruthy();
    });
  });

  it('a name collision with an existing stored recipe is suffixed, never clobbered', async () => {
    mockGitLoadPost();
    // The store already has a recipe saved under "foo.cue".
    vi.mocked(apiGet).mockImplementation(async (path: string) => {
      if (path === '/api/v1/recipes/store') {
        return {
          ok: true,
          json: async () => [{ name: 'foo.cue', path: '/recipes/foo.cue' }],
        } as unknown as Response;
      }
      return defaultApiGet(path);
    });

    renderShell();

    fireEvent.click(await screen.findByRole('button', { name: /load from git/i }));

    const pathInput = await screen.findByLabelText('Recipe filename/path in Repo');
    fireEvent.change(pathInput, { target: { value: 'recipes/foo.cue' } });

    const loadBtn = screen.getByRole('button', { name: 'Load Recipe' });
    fireEvent.click(loadBtn);

    await waitFor(() => {
      expect(apiPost).toHaveBeenCalledWith('/api/v1/recipes/store/foo-2.cue', { content: 'CUE_GIT_CONTENT' });
    });
    expect(apiPost).not.toHaveBeenCalledWith('/api/v1/recipes/store/foo.cue', expect.anything());

    await waitFor(() => {
      expect(useWorkspaceStore.getState().docs['foo-2.cue']).toBeTruthy();
    });
    expect(useWorkspaceStore.getState().docs['foo.cue']).toBeFalsy();
  });
});
