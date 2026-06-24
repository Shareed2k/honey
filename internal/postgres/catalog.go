package postgres

import (
	"context"
	"fmt"
	"time"
)

// CatalogRepository abstracts Postgres system catalog queries.
type CatalogRepository struct {
	pools *PoolManager
}

// NewCatalogRepository creates a new CatalogRepository.
func NewCatalogRepository(pools *PoolManager) *CatalogRepository {
	return &CatalogRepository{pools: pools}
}

// GetDatabases returns a list of non-template database names.
func (r *CatalogRepository) GetDatabases(ctx context.Context, dsn string) ([]string, error) {
	sql := `SELECT datname FROM pg_database WHERE datistemplate = false ORDER BY datname`
	res, err := Query(ctx, r.pools, dsn, sql, nil, QueryOpts{Timeout: 10 * time.Second, Readonly: true})
	if err != nil {
		return nil, fmt.Errorf("postgres catalog: databases: %w", err)
	}

	var dbs []string
	for _, row := range res.Rows {
		if v, ok := row["datname"].(string); ok {
			dbs = append(dbs, v)
		}
	}
	return dbs, nil
}

// GetSchemas returns a list of schema names, excluding pg_catalog and information_schema.
func (r *CatalogRepository) GetSchemas(ctx context.Context, dsn string) ([]string, error) {
	sql := `SELECT schema_name FROM information_schema.schemata WHERE schema_name NOT IN ('pg_catalog','information_schema') ORDER BY schema_name`
	res, err := Query(ctx, r.pools, dsn, sql, nil, QueryOpts{Timeout: 10 * time.Second, Readonly: true})
	if err != nil {
		return nil, fmt.Errorf("postgres catalog: schemas: %w", err)
	}

	var schemas []string
	for _, row := range res.Rows {
		if v, ok := row["schema_name"].(string); ok {
			schemas = append(schemas, v)
		}
	}
	return schemas, nil
}

// GetTables returns a map of schemas to tables, excluding pg_catalog and information_schema.
func (r *CatalogRepository) GetTables(ctx context.Context, dsn string) (map[string][]string, error) {
	sql := `SELECT table_schema, table_name FROM information_schema.tables WHERE table_type='BASE TABLE' AND table_schema NOT IN ('pg_catalog','information_schema') ORDER BY table_schema, table_name`
	res, err := Query(ctx, r.pools, dsn, sql, nil, QueryOpts{Timeout: 10 * time.Second, Readonly: true})
	if err != nil {
		return nil, fmt.Errorf("postgres catalog: tables: %w", err)
	}

	tables := make(map[string][]string)
	for _, row := range res.Rows {
		schema, sok := row["table_schema"].(string)
		table, tok := row["table_name"].(string)
		if sok && tok {
			tables[schema] = append(tables[schema], table)
		}
	}
	return tables, nil
}

// GetColumns returns a map of 'schema.table' to columns, excluding pg_catalog and information_schema.
func (r *CatalogRepository) GetColumns(ctx context.Context, dsn string) (map[string][]string, error) {
	sql := `SELECT table_schema, table_name, column_name FROM information_schema.columns WHERE table_schema NOT IN ('pg_catalog','information_schema') ORDER BY table_schema, table_name, ordinal_position`
	res, err := Query(ctx, r.pools, dsn, sql, nil, QueryOpts{Timeout: 10 * time.Second, Readonly: true})
	if err != nil {
		return nil, fmt.Errorf("postgres catalog: columns: %w", err)
	}

	columns := make(map[string][]string)
	for _, row := range res.Rows {
		schema, sok := row["table_schema"].(string)
		table, tok := row["table_name"].(string)
		column, cok := row["column_name"].(string)
		if sok && tok && cok {
			key := schema + "." + table
			columns[key] = append(columns[key], column)
		}
	}
	return columns, nil
}
