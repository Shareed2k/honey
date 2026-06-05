package anomaly

import (
	"context"
	"net"

	"github.com/shareed2k/honey/internal/jsonutil"
)

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
