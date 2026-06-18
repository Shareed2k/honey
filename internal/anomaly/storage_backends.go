package anomaly

import (
	"bytes"
	"context"
	"fmt"
	"net"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/opensearch-project/opensearch-go/v2"
	"github.com/shareed2k/honey/internal/jsonutil"
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

// ElasticsearchStorage persists records to an OpenSearch index.
type ElasticsearchStorage struct {
	client *opensearch.Client
	index  string
}

// NewElasticsearchStorage connects to an OpenSearch instance via configuration.
func NewElasticsearchStorage(cfg opensearch.Config, index string) (*ElasticsearchStorage, error) {
	client, err := opensearch.NewClient(cfg)
	if err != nil {
		return nil, err
	}
	return &ElasticsearchStorage{client: client, index: index}, nil
}

// Write appends a single record as a bulk batch of one.
func (e *ElasticsearchStorage) Write(ctx context.Context, rec StorageRecord) error {
	return e.WriteBatch(ctx, []StorageRecord{rec})
}

// WriteBatch performs high-speed bulk inserts using the OpenSearch bulk API.
func (e *ElasticsearchStorage) WriteBatch(ctx context.Context, records []StorageRecord) error {
	var buf bytes.Buffer
	for _, rec := range records {
		meta := []byte(fmt.Sprintf(`{ "index" : { "_index" : %q } }%s`, e.index, "\n"))
		buf.Write(meta)

		body, err := jsonutil.Marshal(rec)
		if err != nil {
			return err
		}
		buf.Write(body)
		buf.WriteByte('\n')
	}

	res, err := e.client.Bulk(bytes.NewReader(buf.Bytes()), e.client.Bulk.WithContext(ctx))
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.IsError() {
		return fmt.Errorf("bulk index error: %s", res.Status())
	}
	return nil
}

// Close is a no-op for HTTP client connection.
func (e *ElasticsearchStorage) Close() error {
	return nil
}

// UDPStorage streams JSON-formatted logs over connectionless UDP sockets (fire-and-forget).
type UDPStorage struct {
	conn net.Conn
}

// NewUDPStorage connects to a remote UDP address (e.g. "127.0.0.1:514").
func NewUDPStorage(address string) (*UDPStorage, error) {
	conn, err := net.Dial("udp", address)
	if err != nil {
		return nil, err
	}
	return &UDPStorage{conn: conn}, nil
}

// Write marshals and writes a single JSON record to the UDP socket with a newline delimiter.
func (u *UDPStorage) Write(_ context.Context, rec StorageRecord) error {
	b, err := jsonutil.Marshal(rec)
	if err != nil {
		return err
	}
	_, err = u.conn.Write(append(b, '\n'))
	return err
}

// WriteBatch writes each record in the batch as an individual UDP packet (MTU-friendly).
func (u *UDPStorage) WriteBatch(ctx context.Context, records []StorageRecord) error {
	for _, rec := range records {
		if err := u.Write(ctx, rec); err != nil {
			return err
		}
	}
	return nil
}

// Close closes the UDP socket cleanly.
func (u *UDPStorage) Close() error {
	if u.conn != nil {
		return u.conn.Close()
	}
	return nil
}
