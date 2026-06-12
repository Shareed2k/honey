//go:build integration

package integration

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/shareed2k/honey/internal/anomaly"
)

const testCHTable = "test_anomalies"

func createAnomalyTable(t *testing.T, dsn, tableName string) {
	t.Helper()
	opts, err := clickhouse.ParseDSN(dsn)
	if err != nil {
		t.Fatalf("parse clickhouse DSN: %v", err)
	}
	conn, err := clickhouse.Open(opts)
	if err != nil {
		t.Fatalf("open clickhouse: %v", err)
	}
	defer conn.Close()

	ddl := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s (
			ts      String,
			source  String,
			line    String,
			score   Float64,
			reason  String,
			anomaly Bool
		) ENGINE = MergeTree() ORDER BY ts
	`, tableName)
	if err := conn.Exec(context.Background(), ddl); err != nil {
		t.Fatalf("create anomaly table %s: %v", tableName, err)
	}
}

func TestClickHouseStorage_WriteBatch_RoundTrip(t *testing.T) {
	dsn := startClickHouse(t)
	createAnomalyTable(t, dsn, testCHTable)

	store, err := anomaly.NewClickHouseStorage(dsn, testCHTable)
	if err != nil {
		t.Fatalf("NewClickHouseStorage: %v", err)
	}
	defer store.Close()

	want := anomaly.StorageRecord{
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Source:    "integration-test-roundtrip",
		Line:      "disk usage 95%",
		Score:     0.95,
		Reason:    "high disk",
		Anomaly:   true,
	}
	if err := store.Write(context.Background(), want); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// Read back via raw ClickHouse client.
	opts, _ := clickhouse.ParseDSN(dsn)
	conn, _ := clickhouse.Open(opts)
	defer conn.Close()

	rows, err := conn.Query(context.Background(), fmt.Sprintf(
		"SELECT source, line, score, anomaly FROM %s WHERE source = 'integration-test-roundtrip'",
		testCHTable,
	))
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()

	var found bool
	for rows.Next() {
		var source, line string
		var score float64
		var isAnomaly bool
		if err := rows.Scan(&source, &line, &score, &isAnomaly); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if source == want.Source && line == want.Line && isAnomaly == want.Anomaly {
			found = true
		}
	}
	if !found {
		t.Fatal("inserted record not found in ClickHouse")
	}
}

func TestClickHouseStorage_EmptyTable(t *testing.T) {
	dsn := startClickHouse(t)
	emptyTable := fmt.Sprintf("test_anomalies_empty_%d", time.Now().UnixNano())
	createAnomalyTable(t, dsn, emptyTable)

	store, err := anomaly.NewClickHouseStorage(dsn, emptyTable)
	if err != nil {
		t.Fatalf("NewClickHouseStorage: %v", err)
	}
	defer store.Close()

	// WriteBatch with nil slice must not panic or error.
	if err := store.WriteBatch(context.Background(), nil); err != nil {
		t.Fatalf("WriteBatch(nil): %v", err)
	}
}
