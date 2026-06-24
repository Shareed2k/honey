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
import { lintScript } from './api/exec';

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
    // Shell block detection
    { regex: /(command|script)(\s*:\s*)(""")/, token: ['property', 'operator', 'string'], push: 'shellMultiline' },
    { regex: /(command|script)(\s*:\s*)(")/, token: ['property', 'operator', 'string'], push: 'shellSingle' },
    
    // Standard CUE matching rules
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
  shellMultiline: [
    { regex: /"""/, token: 'string', pop: true },
    { regex: /\b(?:echo|ls|cat|cd|pwd|grep|awk|sed|if|then|else|fi|for|do|done|while|case|esac|sudo|apt|apt-get|yum|apk|dnf|pacman|systemctl|journalctl|docker|kubectl|podman)\b/, token: 'keyword' },
    { regex: /\$\w+|\$\{[^}]+\}/, token: 'variable-2' },
    { regex: /#.*/, token: 'comment' },
    { regex: /"/, token: 'string-2' },
    { regex: /./, token: null }
  ],
  shellSingle: [
    { regex: /"/, token: 'string', pop: true },
    { regex: /\\./, token: 'string-2' },
    { regex: /\b(?:echo|ls|cat|cd|pwd|grep|awk|sed|if|then|else|fi|for|do|done|while|case|esac|sudo|apt|apt-get|yum|apk|dnf|pacman|systemctl|journalctl|docker|kubectl|podman)\b/, token: 'keyword' },
    { regex: /\$\w+|\$\{[^}]+\}/, token: 'variable-2' },
    { regex: /./, token: null }
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

function cueLinter() {
  return linter(
    async (view): Promise<Diagnostic[]> => {
      const doc = view.state.doc;
      const content = doc.toString();
      if (content.trim() === '') return [];

      const diagnostics: Diagnostic[] = [];
      
      // We look for command: or script: followed by a string.
      // This regex matches: 
      // Group 1: The key (command or script)
      // Group 2: The delimiter and opening quote(s) (e.g. `: """` or `: "`)
      // Group 3: The script content
      const regex = /(command|script)\s*:\s*("""|")((?:.|\n)*?)\2/g;
      let match;
      
      while ((match = regex.exec(content)) !== null) {
        const fullMatch = match[0];
        const scriptContent = match[3];
        
        // We only lint if the script actually has content
        if (scriptContent.trim() === '') continue;

        // Calculate the absolute offset where the script content begins in the document.
        // match.index is the start of `command: """`
        // We add the length of the prefix string to get the start of the script text itself.
        const prefixLength = match[1].length + (content.substring(match.index + match[1].length, match.index + fullMatch.length).indexOf(match[2]) + match[2].length);
        
        const scriptStartOffset = match.index + prefixLength;
        const startLine = doc.lineAt(scriptStartOffset).number;

        try {
          // Call the backend shell linter for this specific block
          const res = await lintScript('bash', scriptContent);
          if (!res.available || !res.diagnostics) continue;
          
          for (const d of res.diagnostics) {
            // The linter reports line/col relative to the extracted scriptContent.
            // We map this back to the absolute CodeMirror document positions.
            const absLineNo = startLine + (d.line || 1) - 1;
            
            // Ensure line bounds
            if (absLineNo < 1 || absLineNo > doc.lines) continue;
            
            const lineObj = doc.line(absLineNo);
            const from = Math.min(lineObj.from + Math.max(0, (d.col || 1) - 1), lineObj.to);
            const to = Math.min(lineObj.to, doc.length);
            
            diagnostics.push({
              from,
              to: Math.max(from, to),
              severity: d.severity === 'warning' ? 'warning' : 'error',
              message: d.message || 'syntax error',
            });
          }
        } catch {
          // ignore network errors per block
        }
      }

      return diagnostics;
    },
    { delay: 600 }
  );
}

export default function CodeEditor({ value, onChange, language, lint, height = '260px', placeholder }: Props) {
  const extensions = useMemo<Extension[]>(() => {
    const exts: Extension[] = [EditorView.lineWrapping];
    const langExt = languageExtension(language);
    if (langExt) exts.push(langExt);
    
    if (lint) {
      if (language === 'bash' || language === 'python') {
        exts.push(scriptLinter(language), lintGutter());
      } else if (language === 'cue') {
        exts.push(cueLinter(), lintGutter());
      }
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
