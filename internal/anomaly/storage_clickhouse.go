package anomaly

import (
	"context"
	"fmt"

	"github.com/ClickHouse/clickhouse-go/v2"
)

// ClickHouseStorage persists records to a ClickHouse table.
type ClickHouseStorage struct {
	conn  clickhouse.Conn
	table string
}

// NewClickHouseStorage connects to a ClickHouse instance via DSN.
func NewClickHouseStorage(dsn, table string) (*ClickHouseStorage, error) {
	opts, err := clickhouse.ParseDSN(dsn)
	if err != nil {
		return nil, err
	}
	conn, err := clickhouse.Open(opts)
	if err != nil {
		return nil, err
	}
	return &ClickHouseStorage{conn: conn, table: table}, nil
}

// Write appends a single record as a batch of one.
func (c *ClickHouseStorage) Write(ctx context.Context, rec StorageRecord) error {
	return c.WriteBatch(ctx, []StorageRecord{rec})
}

// WriteBatch performs high-speed bulk inserts using ClickHouse batch prepared statements.
func (c *ClickHouseStorage) WriteBatch(ctx context.Context, records []StorageRecord) error {
	query := fmt.Sprintf("INSERT INTO %s (ts, source, line, score, reason, anomaly)", c.table)
	batch, err := c.conn.PrepareBatch(ctx, query)
	if err != nil {
		return err
	}
	for _, rec := range records {
		err := batch.Append(
			rec.Timestamp,
			rec.Source,
			rec.Line,
			rec.Score,
			rec.Reason,
			rec.Anomaly,
		)
		if err != nil {
			_ = batch.Abort()
			return err
		}
	}
	return batch.Send()
}

// Close closes the database connection cleanly.
func (c *ClickHouseStorage) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}
