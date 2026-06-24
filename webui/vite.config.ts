import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

/** Pull heavy deps out of route/lazy chunks. Most vendor chunks stay under ~500 KiB; `vendor-swagger-ui` is larger by design (~1.3 MiB minified, lazy-loaded API tab). */
function manualChunks(id: string): string | undefined {
  if (!id.includes('node_modules')) {
    return undefined;
  }
  if (id.includes('react-dom') || id.includes('/react/') || id.includes('scheduler')) {
    return 'vendor-react';
  }
  if (id.includes('@xterm')) {
    return 'vendor-xterm';
  }
  if (id.includes('@codemirror') || id.includes('@uiw/react-codemirror') || id.includes('@lezer')) {
    return 'vendor-codemirror';
  }
  if (id.includes('@shikijs/langs')) {
    return 'vendor-shiki-langs';
  }
  if (id.includes('@shikijs/themes')) {
    return 'vendor-shiki-themes';
  }
  if (id.includes('shiki')) {
    return 'vendor-shiki';
  }
  if (
    id.includes('react-markdown') ||
    id.includes('remark-') ||
    id.includes('/remark/') ||
    id.includes('unified') ||
    id.includes('micromark') ||
    id.includes('mdast') ||
    id.includes('hast') ||
    id.includes('vfile') ||
    id.includes('decode-named-character-reference')
  ) {
    return 'vendor-markdown';
  }
  if (id.includes('js-yaml')) {
    return 'vendor-js-yaml';
  }
  if (id.includes('swagger-ui') || id.includes('swagger-client')) {
    return 'vendor-swagger-ui';
  }
  if (id.includes('@ag-ui')) {
    return 'vendor-ag-ui';
  }
  // Remaining deps stay in the chunk that imports them (avoids circular vendor <-> feature splits).
  return undefined;
}

export default defineConfig({
  plugins: [react()],
  base: './',
  build: {
    outDir: '../internal/webserver/static',
    emptyOutDir: true,
    // Allow lazy `vendor-swagger-ui` (~1.3 MiB); keep default-ish limit for other chunks.
    chunkSizeWarningLimit: 1400,
    rollupOptions: {
      output: {
        manualChunks,
      },
    },
  },
  server: {
    port: 5173,
    proxy: {
      '/api': { target: 'http://127.0.0.1:8765', changeOrigin: true },
      '/ws': { target: 'ws://127.0.0.1:8765', ws: true },
    },
  },
});
