import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

/** Pull heavy deps out of route/lazy chunks so no single output file balloons past ~500 KiB. */
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
  // Remaining deps stay in the chunk that imports them (avoids circular vendor <-> feature splits).
  return undefined;
}

export default defineConfig({
  plugins: [react()],
  base: './',
  build: {
    outDir: '../internal/webserver/static',
    emptyOutDir: true,
    chunkSizeWarningLimit: 500,
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
