package secrets

import "context"

// MockResolver is a fixed-value resolver for tests .
type MockResolver struct {
	Value string
	Err   error
}

// Resolve implements [Resolver].
func (m *MockResolver) Resolve(_ context.Context, _ string) (string, error) {
	if m.Err != nil {
		return "", m.Err
	}
	return m.Value, nil
}
