package mobile

// ExecuteRecipe is the gomobile entrypoint.
func ExecuteRecipe(requestJSON string, cb LogCallback) (string, error) {
	_ = requestJSON // unused for now

	if cb != nil {
		cb.OnLog("Initializing honey engine...")
	}

	// Core engine integration will go here.
	// For now, return a dummy successful response.
	return `{"status": "success"}`, nil
}
