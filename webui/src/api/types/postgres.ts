

export interface PostgresCatalog {
  databases: string[];
  schemas: string[];
  tables: Record<string, string[]>;
  columns: Record<string, string[]>;
}

export interface PostgresQueryResponse {
  rows: Record<string, unknown>[];
}