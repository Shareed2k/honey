import { useEffect, useState } from 'react';
import SwaggerUI from 'swagger-ui-react';
import type { SwaggerRequest } from 'swagger-ui-react';
import 'swagger-ui-react/swagger-ui.css';
import { apiHeaders, getToken } from './api/core';

export function OpenApiDocsTab() {
  const [spec, setSpec] = useState<Record<string, unknown> | null>(null);
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    setErr(null);
    setSpec(null);
    void (async () => {
      const res = await fetch('/api/v1/openapi.json', { headers: apiHeaders() });
      if (!res.ok) {
        const msg =
          res.status === 401
            ? 'Unauthorized: add ?token=… to the URL (same as the rest of the UI).'
            : `Failed to load spec (${res.status} ${res.statusText})`;
        if (!cancelled) {
          setErr(msg);
        }
        return;
      }
      try {
        const j = (await res.json()) as Record<string, unknown>;
        if (!cancelled) {
          setSpec(j);
        }
      } catch (e) {
        if (!cancelled) {
          setErr(e instanceof Error ? e.message : String(e));
        }
      }
    })();
    return () => {
      cancelled = true;
    };
  }, []);

  if (err) {
    return <p style={{ color: '#f66' }}>{err}</p>;
  }
  if (!spec) {
    return <p style={{ opacity: 0.85 }}>Loading API reference…</p>;
  }

  return (
    <section style={{ marginTop: '0.5rem' }}>
      <p style={{ fontSize: '0.85rem', opacity: 0.85, marginBottom: '0.75rem' }}>
        Interactive docs from <code>/api/v1/openapi.json</code>. &quot;Try it out&quot; uses the same token as this UI (
        <code>Authorization</code> / <code>X-Honey-Token</code>).
      </p>
      <div
        className="honey-openapi-swagger"
        style={{
          border: '1px solid rgba(255,255,255,0.12)',
          borderRadius: 8,
          overflow: 'hidden',
          background: '#1a1d24',
        }}
      >
        <SwaggerUI
          spec={spec}
          deepLinking
          tryItOutEnabled
          docExpansion="list"
          defaultModelsExpandDepth={1}
          requestInterceptor={(req: SwaggerRequest): SwaggerRequest => {
            const t = getToken();
            const next = { ...req };
            next.headers = { ...(next.headers as Record<string, string>) };
            if (t) {
              next.headers.Authorization = `Bearer ${t}`;
              next.headers['X-Honey-Token'] = t;
            }
            return next;
          }}
        />
      </div>
    </section>
  );
}
