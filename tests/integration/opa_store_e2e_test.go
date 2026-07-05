//go:build integration

package integration

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/webserver"
)

// denyStoreMutations allows api_request (auth middleware) but denies recipe_save
// and recipe_delete so the store OPA gate can be tested in isolation.
const denyStoreMutations = `package honey
import rego.v1
default allow := true
allow := false if { input.action == "recipe_save" }
allow := false if { input.action == "recipe_delete" }
`

// denyStoreReads allows api_request but denies recipe_read and recipe_list.
const denyStoreReads = `package honey
import rego.v1
default allow := true
allow := false if { input.action == "recipe_read" }
allow := false if { input.action == "recipe_list" }
`

func TestOPAE2E_StoreWrite_Deny(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfg := &config.File{}
	cfg.Defaults.Studio.RecipesPath = dir
	enf := newEnforcer(t, denyStoreMutations)

	base := newTestServer(t, webserver.Options{
		Enforcer: enf,
		Config:   cfg,
	})

	req, err := http.NewRequest(http.MethodPost, base+"/api/v1/recipes/store/test.cue",
		strings.NewReader(`{"content":"package honey\n"}`))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()

	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
}

func TestOPAE2E_StoreDelete_Deny(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "test.cue"), []byte("package honey\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := &config.File{}
	cfg.Defaults.Studio.RecipesPath = dir
	enf := newEnforcer(t, denyStoreMutations)

	base := newTestServer(t, webserver.Options{
		Enforcer: enf,
		Config:   cfg,
	})

	req, err := http.NewRequest(http.MethodDelete, base+"/api/v1/recipes/store/test.cue", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer test-token")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()

	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
}

func TestOPAE2E_StoreGet_Deny(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfg := &config.File{}
	cfg.Defaults.Studio.RecipesPath = dir
	enf := newEnforcer(t, denyStoreReads)

	base := newTestServer(t, webserver.Options{
		Enforcer: enf,
		Config:   cfg,
	})

	req, err := http.NewRequest(http.MethodGet, base+"/api/v1/recipes/store/test.cue", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer test-token")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()

	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
}

func TestOPAE2E_StoreList_Deny(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfg := &config.File{}
	cfg.Defaults.Studio.RecipesPath = dir
	enf := newEnforcer(t, denyStoreReads)

	base := newTestServer(t, webserver.Options{
		Enforcer: enf,
		Config:   cfg,
	})

	req, err := http.NewRequest(http.MethodGet, base+"/api/v1/recipes/store/", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer test-token")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()

	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
}

func TestOPAE2E_StoreWrite_Allow(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfg := &config.File{}
	cfg.Defaults.Studio.RecipesPath = dir
	enf := newEnforcer(t, "package honey\nimport rego.v1\ndefault allow := true\n")

	base := newTestServer(t, webserver.Options{
		Enforcer: enf,
		Config:   cfg,
	})

	req, err := http.NewRequest(http.MethodPost, base+"/api/v1/recipes/store/test.cue",
		strings.NewReader(`{"content":"package honey\n"}`))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
}
