import { apiGet, apiPost } from './core';
import { PostgresCatalog, PostgresQueryResponse } from './types/postgres';



export async function fetchPostgresCatalog(sessionId: string): Promise<PostgresCatalog> {
  const res = await apiGet(`/api/v1/postgres/catalog?session_id=${encodeURIComponent(sessionId)}`);
  if (!res.ok) {
    const errorText = await res.text();
    throw new Error(errorText || res.statusText);
  }
  return await res.json();
}

export async function runPostgresQuery(sessionId: string, sql: string, database?: string): Promise<PostgresQueryResponse> {
  const res = await apiPost('/api/v1/postgres/query', {
    session_id: sessionId,
    sql,
    database,
    readonly: true,
    timeout_ms: 15000,
    limit: 1000,
  });
  if (!res.ok) {
    const errorText = await res.text();
    throw new Error(errorText || res.statusText);
  }
  return await res.json();
}