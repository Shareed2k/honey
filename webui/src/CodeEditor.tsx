import CodeMirror from '@uiw/react-codemirror';
import { EditorView } from '@codemirror/view';
import { Diagnostic, linter, lintGutter } from '@codemirror/lint';
import { StreamLanguage } from '@codemirror/language';
import { shell } from '@codemirror/legacy-modes/mode/shell';
import { simpleMode } from '@codemirror/legacy-modes/mode/simple-mode';
import { python } from '@codemirror/lang-python';
import { oneDark } from '@codemirror/theme-one-dark';
import type { Extension } from '@codemirror/state';
import { useMemo } from 'react';
import { lintScript } from './api';

export type EditorLanguage = 'bash' | 'python' | 'plain' | 'cue';

type Props = {
  value: string;
  onChange: (next: string) => void;
  language: EditorLanguage;
  /** When true and language is bash/python, run server-side syntax lint. */
  lint?: boolean;
  height?: string;
  placeholder?: string;
};

const cueMode = simpleMode({
  start: [
    { regex: /\/\/.*/, token: 'comment' },
    { regex: /"(?:[^\\]|\\.)*?(?:"|$)/, token: 'string' },
    { regex: /'''(?:[^']|'[^']|''[^'])*?(?:'''|$)/, token: 'string' },
    { regex: /"""(?:[^"]|"[^"]|""[^"])*?(?:"""|$)/, token: 'string' },
    { regex: /\b(?:package|import|for|if|in|let|null|true|false)\b/, token: 'keyword' },
    { regex: /\b(?:0|[1-9][0-9]*)(?:\.[0-9]+)?\b/, token: 'number' },
    { regex: /==|!=|<=|>=|&&|\|\||[+\-*/%<>=!]/, token: 'operator' },
    { regex: /[a-zA-Z_][a-zA-Z0-9_]*:/, token: 'property' },
    { regex: /[[\]{}(),]/, token: 'punctuation' },
  ],
  languageData: {
    commentTokens: { line: '//' },
  },
});

function languageExtension(language: EditorLanguage): Extension | null {
  switch (language) {
    case 'python':
      return python();
    case 'bash':
      return StreamLanguage.define(shell);
    case 'cue':
      return StreamLanguage.define(cueMode);
    default:
      return null;
  }
}

// scriptLinter calls the backend lint endpoint and maps diagnostics into the editor.
// CodeMirror debounces invocations via the linter delay.
function scriptLinter(language: 'bash' | 'python') {
  return linter(
    async (view): Promise<Diagnostic[]> => {
      const doc = view.state.doc;
      const content = doc.toString();
      if (content.trim() === '') return [];
      let res;
      try {
        res = await lintScript(language, content);
      } catch {
        return []; // network/endpoint error → no markers
      }
      if (!res.available || !res.diagnostics) return [];
      const max = doc.length;
      return res.diagnostics.flatMap((d) => {
        const lineNo = Math.max(1, Math.min(d.line || 1, doc.lines));
        const line = doc.line(lineNo);
        const from = Math.min(line.from + Math.max(0, (d.col || 1) - 1), line.to);
        const to = Math.min(line.to, max);
        return [{
          from,
          to: Math.max(from, to),
          severity: d.severity === 'warning' ? 'warning' : 'error',
          message: d.message || 'syntax error',
        } as Diagnostic];
      });
    },
    { delay: 600 },
  );
}

export default function CodeEditor({ value, onChange, language, lint, height = '260px', placeholder }: Props) {
  const extensions = useMemo<Extension[]>(() => {
    const exts: Extension[] = [EditorView.lineWrapping];
    const langExt = languageExtension(language);
    if (langExt) exts.push(langExt);
    if (lint && (language === 'bash' || language === 'python')) {
      exts.push(scriptLinter(language), lintGutter());
    }
    return exts;
  }, [language, lint]);

  return (
    <CodeMirror
      value={value}
      height={height}
      theme={oneDark}
      extensions={extensions}
      onChange={onChange}
      placeholder={placeholder}
      basicSetup={{
        lineNumbers: true,
        highlightActiveLine: true,
        bracketMatching: true,
        foldGutter: true,
        autocompletion: false,
      }}
    />
  );
}
