import { useEffect, useState } from 'react';
import cueGrammar from './shiki/cue.tmLanguage.json';
import { createHighlighterCore } from 'shiki/core';
import { createJavaScriptRegexEngine } from 'shiki/engine/javascript';
import yamlGrammar from '@shikijs/langs/yaml';
import githubDarkTheme from '@shikijs/themes/github-dark';

type SupportedLanguage = 'yaml' | 'cue';

type Props = {
  code: string;
  language: SupportedLanguage;
  className?: string;
};

const themeName = 'github-dark';

let highlighterPromise: ReturnType<typeof createHighlighterCore> | null = null;

function getHighlighter() {
  if (!highlighterPromise) {
    highlighterPromise = createHighlighterCore({
      themes: [githubDarkTheme],
      langs: [yamlGrammar, cueGrammar],
      langAlias: {
        yml: 'yaml',
      },
      engine: createJavaScriptRegexEngine({ forgiving: true }),
    });
  }
  return highlighterPromise;
}

function escapeHtml(text: string): string {
  return text
    .replaceAll('&', '&amp;')
    .replaceAll('<', '&lt;')
    .replaceAll('>', '&gt;')
    .replaceAll('"', '&quot;')
    .replaceAll("'", '&#39;');
}

export function HighlightedCode({ code, language, className }: Props) {
  const [html, setHtml] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    void (async () => {
      try {
        const highlighter = await getHighlighter();
        const nextHtml = highlighter.codeToHtml(code, {
          lang: language,
          theme: themeName,
        });
        if (!cancelled) {
          setHtml(nextHtml);
        }
      } catch {
        if (!cancelled) {
          setHtml(`<pre>${escapeHtml(code)}</pre>`);
        }
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [code, language]);

  return (
    <div
      className={className ? `code-highlight ${className}` : 'code-highlight'}
      dangerouslySetInnerHTML={{ __html: html ?? `<pre>${escapeHtml(code)}</pre>` }}
    />
  );
}
