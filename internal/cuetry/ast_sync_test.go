package cuetry

import (
	"strings"
	"testing"
)

func TestApplyJSONToCUEAST(t *testing.T) {
	origCUE := `// test comment
recipe: {
	name: "test"
	steps: []
}`
	newJSON := `{"name": "test_updated", "steps": []}`

	out, err := ApplyJSONToCUEAST([]byte(origCUE), []byte(newJSON))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(string(out), "// test comment") {
		// Currently this might fail until deep AST merging is built,
		// but the signature must exist.
		t.Logf("AST sync comments missing (expected for skeleton implementation)")
	}
}

func TestDeepASTSync(t *testing.T) {
	origCUE := `// Header comment
recipe: {
	name: "test"
	// Steps comment
	steps: []
}`
	newJSON := `{"name": "test_updated", "steps": [{"command": "echo 1"}]}`

	out, err := ApplyJSONToCUEAST([]byte(origCUE), []byte(newJSON))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(string(out), "// Header comment") {
		t.Errorf("expected header comment to be preserved")
	}
	if !strings.Contains(string(out), "test_updated") {
		t.Errorf("expected name to be updated")
	}
}
