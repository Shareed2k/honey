import { useEffect, useMemo, useState, type ComponentPropsWithoutRef } from 'react';
import ReactMarkdown, { defaultUrlTransform, type Components } from 'react-markdown';
import remarkGfm from 'remark-gfm';
import bashGrammar from '@shikijs/langs/bash';
import javascriptGrammar from '@shikijs/langs/javascript';
import jsonGrammar from '@shikijs/langs/json';
import typescriptGrammar from '@shikijs/langs/typescript';
import yamlGrammar from '@shikijs/langs/yaml';
import githubDarkTheme from '@shikijs/themes/github-dark';
import { createHighlighterCore } from 'shiki/core';
import { createJavaScriptRegexEngine } from 'shiki/engine/javascript';
import cueGrammar from './shiki/cue.tmLanguage.json';

const themeName = 'github-dark';

function escapeHtml(text: string): string {
  return text
    .replaceAll('&', '&amp;')
    .replaceAll('<', '&lt;')
    .replaceAll('>', '&gt;')
    .replaceAll('"', '&quot;')
    .replaceAll("'", '&#39;');
}

/** Map fenced-block language tag to a Shiki grammar id we bundle. */
function resolveShikiLang(fence: string): string | null {
  const raw = fence.trim().toLowerCase();
  if (!raw) return null;
  const table: Record<string, string> = {
    bash: 'bash',
    sh: 'bash',
    shell: 'bash',
    zsh: 'bash',
    yaml: 'yaml',
    yml: 'yaml',
    json: 'json',
    typescript: 'typescript',
    ts: 'typescript',
    tsx: 'typescript',
    javascript: 'javascript',
    js: 'javascript',
    jsx: 'javascript',
    cue: 'cue',
  };
  return table[raw] ?? null;
}

let aiHighlighterPromise: ReturnType<typeof createHighlighterCore> | null = null;

function getAiHighlighter() {
  if (!aiHighlighterPromise) {
    aiHighlighterPromise = createHighlighterCore({
      themes: [githubDarkTheme],
      langs: [bashGrammar, yamlGrammar, jsonGrammar, typescriptGrammar, javascriptGrammar, cueGrammar],
      langAlias: {
        yml: 'yaml',
        sh: 'bash',
        shell: 'bash',
        zsh: 'bash',
        ts: 'typescript',
        tsx: 'typescript',
        js: 'javascript',
        jsx: 'javascript',
      },
      engine: createJavaScriptRegexEngine({ forgiving: true }),
    });
  }
  return aiHighlighterPromise;
}

function FencedShikiCode({ lang, code }: { lang: string; code: string }) {
  const resolved = resolveShikiLang(lang);
  const [html, setHtml] = useState<string | null>(null);

  useEffect(() => {
    if (resolved === null) return;
    let cancelled = false;
    void (async () => {
      try {
        const highlighter = await getAiHighlighter();
        const nextHtml = highlighter.codeToHtml(code, {
          lang: resolved,
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
  }, [resolved, code]);

  if (resolved === null) {
    return (
      <pre className="ai-markdown-plain-pre">
        <code>{code}</code>
      </pre>
    );
  }

  return (
    <div
      className="code-highlight ai-markdown-fenced"
      dangerouslySetInnerHTML={{ __html: html ?? `<pre>${escapeHtml(code)}</pre>` }}
    />
  );
}

function SafeAnchor({ href, children, ...rest }: ComponentPropsWithoutRef<'a'>) {
  if (!href) {
    return <span className="ai-markdown-link-stub">{children}</span>;
  }
  const trimmed = href.trim();
  const lower = trimmed.toLowerCase();
  const allowed =
    lower.startsWith('https://') || lower.startsWith('http://') || lower.startsWith('mailto:');
  if (!allowed) {
    return <span className="ai-markdown-link-stub">{children}</span>;
  }
  const isHttp = lower.startsWith('http://') || lower.startsWith('https://');
  return (
    <a href={trimmed} rel="noopener noreferrer" target={isHttp ? '_blank' : undefined} {...rest}>
      {children}
    </a>
  );
}

function buildComponents(): Components {
  return {
    pre: ({ children }) => <div className="ai-markdown-pre-host">{children}</div>,
    code: ({ className, children, ...props }) => {
      const codeText = String(children).replace(/\n$/, '');
      const langMatch = /language-([\w-]+)/.exec(className || '');
      if (langMatch) {
        return <FencedShikiCode lang={langMatch[1]} code={codeText} />;
      }
      if (codeText.includes('\n')) {
        return (
          <pre className="ai-markdown-plain-pre">
            <code>{codeText}</code>
          </pre>
        );
      }
      return (
        <code className="ai-markdown-inline-code" {...props}>
          {children}
        </code>
      );
    },
    a: ({ href, children, ...rest }) => (
      <SafeAnchor href={href} {...rest}>
        {children}
      </SafeAnchor>
    ),
  };
}

type Props = {
  content: string;
  className?: string;
};

export function AiMarkdown({ content, className }: Props) {
  const components = useMemo(() => buildComponents(), []);
  const rootClass = ['ai-markdown', className].filter(Boolean).join(' ');

  return (
    <div className={rootClass}>
      <ReactMarkdown remarkPlugins={[remarkGfm]} components={components} urlTransform={defaultUrlTransform}>
        {content}
      </ReactMarkdown>
    </div>
  );
}
