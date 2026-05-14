import CodeMirror from '@uiw/react-codemirror';
import { keymap } from '@codemirror/view';
import { Diagnostic, linter, lintGutter } from '@codemirror/lint';
import { yaml } from '@codemirror/lang-yaml';
import { oneDark } from '@codemirror/theme-one-dark';
import { useEffect, useMemo } from 'react';
import { isMap, isScalar, isSeq, parseDocument, YAMLMap, YAMLSeq } from 'yaml';
import type { ConfigSchemaFieldSpec, ConfigUISchema } from './api';

type Props = {
  value: string;
  onChange: (next: string) => void;
  onSave: () => void;
  schema: ConfigUISchema | null;
  onLintStateChange?: (hasBlockingIssue: boolean) => void;
};

type Severity = 'error' | 'warning';

function clampRange(from: number, to: number, max: number): { from: number; to: number } {
  const safeFrom = Math.max(0, Math.min(from, max));
  const safeTo = Math.max(safeFrom + 1, Math.min(to, max));
  return { from: safeFrom, to: safeTo };
}

function nodeRange(node: unknown, fallbackTo = 1): { from: number; to: number } {
  const maybeNode = node as { range?: unknown } | null | undefined;
  const maybeRange = maybeNode?.range;
  if (Array.isArray(maybeRange) && maybeRange.length >= 2) {
    const from = typeof maybeRange[0] === 'number' ? maybeRange[0] : 0;
    const to = typeof maybeRange[1] === 'number' ? maybeRange[1] : from + 1;
    return { from, to: Math.max(from + 1, to) };
  }
  return { from: 0, to: fallbackTo };
}

function pushDiag(
  diagnostics: Diagnostic[],
  message: string,
  severity: Severity,
  from: number,
  to: number,
  maxLen: number,
): void {
  const range = clampRange(from, to, maxLen);
  diagnostics.push({
    from: range.from,
    to: range.to,
    severity,
    message,
  });
}

function scalarString(node: unknown): string | null {
  if (!isScalar(node)) {
    return null;
  }
  return typeof node.value === 'string' ? node.value : null;
}

function fieldsByKey(fields: ConfigSchemaFieldSpec[]): Map<string, ConfigSchemaFieldSpec> {
  return new Map(fields.map((f) => [f.key, f]));
}

function lintHoneyConfig(text: string, schema: ConfigUISchema | null): Diagnostic[] {
  const diagnostics: Diagnostic[] = [];
  const doc = parseDocument(text, { strict: false });

  if (doc.errors.length > 0) {
    for (const err of doc.errors) {
      const pos = err.pos ?? [0, 1];
      pushDiag(diagnostics, err.message, 'error', pos[0], pos[1], text.length);
    }
    return diagnostics;
  }

  if (!doc.contents || !isMap(doc.contents)) {
    const r = nodeRange(doc.contents ?? undefined);
    pushDiag(diagnostics, 'Config root must be a YAML mapping/object.', 'error', r.from, r.to, text.length);
    return diagnostics;
  }

  if (!schema) {
    return diagnostics;
  }

  const root = doc.contents as YAMLMap<unknown, unknown>;
  const rootAllowed = new Set(schema.top_level_keys || []);
  const defaultFieldMap = fieldsByKey(schema.defaults || []);

  for (const pair of root.items) {
    const key = scalarString(pair.key);
    if (!key) {
      continue;
    }
    if (!rootAllowed.has(key)) {
      const r = nodeRange(pair.key);
      pushDiag(
        diagnostics,
        `Unknown top-level key "${key}". Allowed keys: ${Array.from(rootAllowed).join(', ')}.`,
        'warning',
        r.from,
        r.to,
        text.length,
      );
    }
  }

  for (const pair of root.items) {
    const key = scalarString(pair.key);
    if (!key) {
      continue;
    }

    if (key === 'version') {
      if (!isScalar(pair.value) || typeof pair.value.value !== 'number' || !Number.isInteger(pair.value.value)) {
        const r = nodeRange(pair.value ?? pair.key);
        pushDiag(diagnostics, 'version must be an integer.', 'error', r.from, r.to, text.length);
      }
      continue;
    }

    if (key === 'defaults') {
      if (!isMap(pair.value)) {
        const r = nodeRange(pair.value ?? pair.key);
        pushDiag(diagnostics, 'defaults must be a mapping/object.', 'error', r.from, r.to, text.length);
        continue;
      }
      const defaultsMap = pair.value as YAMLMap<unknown, unknown>;
      for (const item of defaultsMap.items) {
        const k = scalarString(item.key);
        if (!k) {
          continue;
        }
        const fieldSpec = defaultFieldMap.get(k);
        if (!fieldSpec) {
          const r = nodeRange(item.key);
          pushDiag(
            diagnostics,
            `Unknown defaults key "${k}".`,
            'warning',
            r.from,
            r.to,
            text.length,
          );
          continue;
        }
        if (!isScalar(item.value)) {
          const r = nodeRange(item.value ?? item.key);
          pushDiag(diagnostics, `defaults.${k} must be a ${fieldSpec.type}.`, 'error', r.from, r.to, text.length);
          continue;
        }
        if (fieldSpec.type === 'string' && typeof item.value.value !== 'string') {
          const r = nodeRange(item.value ?? item.key);
          pushDiag(diagnostics, `defaults.${k} must be a string.`, 'error', r.from, r.to, text.length);
        }
        if (fieldSpec.type === 'boolean' && typeof item.value.value !== 'boolean') {
          const r = nodeRange(item.value ?? item.key);
          pushDiag(diagnostics, `defaults.${k} must be a boolean.`, 'error', r.from, r.to, text.length);
        }
        if (fieldSpec.type === 'integer' && (typeof item.value.value !== 'number' || !Number.isInteger(item.value.value))) {
          const r = nodeRange(item.value ?? item.key);
          pushDiag(diagnostics, `defaults.${k} must be an integer.`, 'error', r.from, r.to, text.length);
        }
        if (fieldSpec.enum && fieldSpec.enum.length > 0 && typeof item.value.value === 'string' && !fieldSpec.enum.includes(item.value.value)) {
          const r = nodeRange(item.value ?? item.key);
          pushDiag(
            diagnostics,
            `defaults.${k} should be one of: ${fieldSpec.enum.join(', ')}.`,
            fieldSpec.enum_as_warning ? 'warning' : 'error',
            r.from,
            r.to,
            text.length,
          );
        }
      }
      continue;
    }

    if (key === 'backends') {
      if (!isMap(pair.value)) {
        const r = nodeRange(pair.value ?? pair.key);
        pushDiag(diagnostics, 'backends must be a mapping/object.', 'error', r.from, r.to, text.length);
        continue;
      }
      const backendsMap = pair.value as YAMLMap<unknown, unknown>;
      for (const backendPair of backendsMap.items) {
        const backendKey = scalarString(backendPair.key);
        if (!backendKey) {
          continue;
        }
        const backendSchema = schema.backends[backendKey];
        if (!backendSchema) {
          const r = nodeRange(backendPair.key);
          pushDiag(
            diagnostics,
            `Unknown backends key "${backendKey}".`,
            'warning',
            r.from,
            r.to,
            text.length,
          );
          continue;
        }

        if (!isSeq(backendPair.value)) {
          const r = nodeRange(backendPair.value ?? backendPair.key);
          pushDiag(
            diagnostics,
            `backends.${backendKey} must be a list/array.`,
            'error',
            r.from,
            r.to,
            text.length,
          );
          continue;
        }

        const seq = backendPair.value as YAMLSeq<unknown>;
        const fieldMap = fieldsByKey(backendSchema.fields);
        seq.items.forEach((entry, index) => {
          if (!isMap(entry)) {
            const r = nodeRange(entry ?? backendPair.key);
            pushDiag(
              diagnostics,
              `backends.${backendKey}[${index}] must be a mapping/object.`,
              'error',
              r.from,
              r.to,
              text.length,
            );
            return;
          }

          const entryMap = entry as YAMLMap<unknown, unknown>;
          for (const fieldPair of entryMap.items) {
            const fieldKey = scalarString(fieldPair.key);
            if (!fieldKey) {
              continue;
            }
            const fieldSpec = fieldMap.get(fieldKey);
            if (!fieldSpec) {
              const r = nodeRange(fieldPair.key);
              pushDiag(
                diagnostics,
                `Unknown field "${fieldKey}" in backends.${backendKey}[${index}].`,
                'warning',
                r.from,
                r.to,
                text.length,
              );
              continue;
            }
            if (!isScalar(fieldPair.value)) {
              const r = nodeRange(fieldPair.value ?? fieldPair.key);
              pushDiag(
                diagnostics,
                `backends.${backendKey}[${index}].${fieldKey} must be a ${fieldSpec.type}.`,
                'error',
                r.from,
                r.to,
                text.length,
              );
              continue;
            }
            if (fieldSpec.type === 'string' && typeof fieldPair.value.value !== 'string') {
              const r = nodeRange(fieldPair.value);
              pushDiag(
                diagnostics,
                `backends.${backendKey}[${index}].${fieldKey} must be a string.`,
                'error',
                r.from,
                r.to,
                text.length,
              );
            }
            if (fieldSpec.type === 'boolean' && typeof fieldPair.value.value !== 'boolean') {
              const r = nodeRange(fieldPair.value);
              pushDiag(
                diagnostics,
                `backends.${backendKey}[${index}].${fieldKey} must be a boolean.`,
                'error',
                r.from,
                r.to,
                text.length,
              );
            }
            if (fieldSpec.type === 'integer' && (typeof fieldPair.value.value !== 'number' || !Number.isInteger(fieldPair.value.value))) {
              const r = nodeRange(fieldPair.value);
              pushDiag(
                diagnostics,
                `backends.${backendKey}[${index}].${fieldKey} must be an integer.`,
                'error',
                r.from,
                r.to,
                text.length,
              );
            }
            if (fieldSpec.enum && fieldSpec.enum.length > 0 && typeof fieldPair.value.value === 'string') {
              if (!fieldSpec.enum.includes(fieldPair.value.value)) {
                const r = nodeRange(fieldPair.value);
                pushDiag(
                  diagnostics,
                  `backends.${backendKey}[${index}].${fieldKey} should be one of: ${fieldSpec.enum.join(', ')}.`,
                  fieldSpec.enum_as_warning ? 'warning' : 'error',
                  r.from,
                  r.to,
                  text.length,
                );
              }
            }
          }
        });
      }
    }
  }

  return diagnostics;
}

export function RawYamlEditor({ value, onChange, onSave, schema, onLintStateChange }: Props) {
  const diagnostics = useMemo(() => lintHoneyConfig(value, schema), [value, schema]);
  const hasBlockingIssues = diagnostics.some((d) => d.severity === 'error' || d.severity === 'warning');

  const saveShortcut = useMemo(
    () =>
      keymap.of([
        {
          key: 'Mod-s',
          run: () => {
            if (!hasBlockingIssues) {
              onSave();
            }
            return true;
          },
        },
      ]),
    [hasBlockingIssues, onSave],
  );

  const yamlParseLinter = useMemo(
    () => linter((view) => lintHoneyConfig(view.state.doc.toString(), schema)),
    [schema],
  );

  useEffect(() => {
    if (!onLintStateChange) {
      return;
    }
    onLintStateChange(hasBlockingIssues);
  }, [hasBlockingIssues, onLintStateChange]);

  return (
    <div className="raw-yaml-editor">
      <CodeMirror
        value={value}
        height="420px"
        theme={oneDark}
        extensions={[yaml(), yamlParseLinter, lintGutter(), saveShortcut]}
        onChange={(nextValue) => onChange(nextValue)}
        basicSetup={{
          lineNumbers: true,
          highlightActiveLine: true,
          highlightActiveLineGutter: true,
          foldGutter: true,
          autocompletion: true,
          bracketMatching: true,
        }}
      />
    </div>
  );
}
