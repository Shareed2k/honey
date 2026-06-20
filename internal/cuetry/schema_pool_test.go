package cuetry

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseRemoteRecipe_ConcurrentPooled(t *testing.T) {
	src := []byte(`recipe: { name: "p", steps: [{host: "10.0.0.1", command: "echo hi"}] }`)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := ParseRemoteRecipe(src, nil)
			require.NoError(t, err)
			require.Equal(t, "p", r.Name)
		}()
	}
	wg.Wait()
}

func TestParseRemoteRecipe_InvalidStillFails(t *testing.T) {
	_, err := ParseRemoteRecipe([]byte(`recipe: { name: 123 }`), nil)
	require.Error(t, err)
}
