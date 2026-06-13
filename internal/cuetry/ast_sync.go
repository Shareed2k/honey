package cuetry

import (
	"fmt"

	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/format"
	"cuelang.org/go/cue/parser"
)

// ApplyJSONToCUEAST is the boundary for mapping JSON structural edits back onto raw CUE.
func ApplyJSONToCUEAST(originalCUE []byte, updatedJSON []byte) ([]byte, error) {
	if len(originalCUE) == 0 {
		return updatedJSON, nil
	}

	f, err := parser.ParseFile("recipe.cue", originalCUE, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("failed to parse original CUE: %w", err)
	}

	// Simple heuristic: parse the updated JSON as a CUE AST
	// Since JSON is valid CUE, we can parse it to extract the AST nodes!
	fUpdated, err := parser.ParseFile("updated.json", updatedJSON, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("failed to parse updated JSON as CUE: %w", err)
	}

	// We want the `recipe: { ... }` block from fUpdated. Wait, the JSON *is* the recipe block contents.
	// In the JSON we have {"name": "test_updated", "steps": [...]}, which are the struct fields inside `recipe: {}`.

	var updatedFields []interface{}
	for _, decl := range fUpdated.Decls {
		if x, ok := decl.(*ast.Field); ok {
			updatedFields = append(updatedFields, x)
		}
	}
	_ = updatedFields // Silence unused variable warning if not used below

	ast.Walk(f, func(n ast.Node) bool {
		if x, ok := n.(*ast.Field); ok {
			if ident, ok := x.Label.(*ast.Ident); ok && ident.Name == "recipe" {
				if structLit, ok := x.Value.(*ast.StructLit); ok {
					// We need to merge or replace fields.
					// Let's replace the whole `recipe: {}` contents but attempt to rescue
					// existing comments from the old fields if possible, or at least keep the top-level
					// comments attached to the `recipe` identifier.

					// Simplest deep sync strategy for MVP: replace the struct fields entirely with the JSON fields.
					// The top-level comments (e.g. `// Header comment`) remain intact because they are attached to
					// the `recipe` node or the file itself.
					structLit.Elts = nil
					structLit.Elts = append(structLit.Elts, fUpdated.Decls...)
				}
			}
		}
		return true
	}, nil)

	b, err := format.Node(f)
	if err != nil {
		return nil, fmt.Errorf("failed to format CUE AST: %w", err)
	}
	return b, nil
}
