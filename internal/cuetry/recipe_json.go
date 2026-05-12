package cuetry

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// CanonicalRecipeJSON returns deterministic JSON (sorted keys, no extra
// whitespace) for the given Recipe. Two Recipes that resolve to the same plan
// produce the same bytes here.
func CanonicalRecipeJSON(r Recipe) ([]byte, error) {
	raw, err := json.Marshal(r)
	if err != nil {
		return nil, fmt.Errorf("recipe canonical json: marshal: %w", err)
	}
	var generic any
	if err := json.Unmarshal(raw, &generic); err != nil {
		return nil, fmt.Errorf("recipe canonical json: reparse: %w", err)
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(generic); err != nil {
		return nil, fmt.Errorf("recipe canonical json: re-encode: %w", err)
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// RecipeFromJSON deserializes a canonical (or near-canonical) JSON payload back
// into a Recipe value. Run cuetry.ValidateRemoteRecipe (or the equivalent
// per-step validators) after this to ensure the result is well-formed.
func RecipeFromJSON(raw []byte) (Recipe, error) {
	var r Recipe
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&r); err != nil {
		return Recipe{}, fmt.Errorf("recipe from json: %w", err)
	}
	return r, nil
}

// HashRecipeJSON returns "sha256:" + hex(sha256(CanonicalRecipeJSON(r))).
// Used to compare a recording's recipe to a disk recipe and decide "edited?".
func HashRecipeJSON(r Recipe) (string, error) {
	b, err := CanonicalRecipeJSON(r)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}
