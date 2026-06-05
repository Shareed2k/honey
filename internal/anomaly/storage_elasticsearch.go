package anomaly

import (
	"bytes"
	"context"
	"fmt"

	"github.com/opensearch-project/opensearch-go/v2"

	"github.com/shareed2k/honey/internal/jsonutil"
)

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
