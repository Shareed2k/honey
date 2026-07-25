import js from '@eslint/js';
import globals from 'globals';
import reactHooks from 'eslint-plugin-react-hooks';
import reactRefresh from 'eslint-plugin-react-refresh';
import tseslint from 'typescript-eslint';

export default tseslint.config(
  // Generated build output and tooling are not linted.
  { ignores: ['dist', 'node_modules', '../internal/webserver/static'] },
  {
    files: ['**/*.{ts,tsx}'],
    extends: [js.configs.recommended, ...tseslint.configs.recommended],
    languageOptions: {
      ecmaVersion: 2022,
      globals: globals.browser,
    },
    plugins: {
      'react-hooks': reactHooks,
      'react-refresh': reactRefresh,
    },
    rules: {
      // react-hooks 7's `recommended` preset pulls in the new react-compiler
      // rules (set-state-in-effect, immutability, refs, ...). Keep only the two
      // classic rules to preserve the prior (react-hooks 5) lint behavior;
      // adopting the compiler rules is a separate, deliberate refactor.
      'react-hooks/rules-of-hooks': 'error',
      'react-hooks/exhaustive-deps': 'warn',
      'react-refresh/only-export-components': ['warn', { allowConstantExport: true }],
      '@typescript-eslint/no-unused-vars': ['warn', { argsIgnorePattern: '^_', varsIgnorePattern: '^_' }],
    },
  },
  // Test files run under node + vitest globals.
  {
    files: ['**/*.test.{ts,tsx}', 'src/setupTests.ts'],
    languageOptions: { globals: { ...globals.node, ...globals.vitest } },
  },
);
