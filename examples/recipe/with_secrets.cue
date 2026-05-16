// Recipe secrets are symmetric values only: secure:v1:<b64-nonce>:<b64-ciphertext>.
// Build ciphertext with the same AES-256 data key as your honey stack (defaults.secretsprovider +
// defaults.encryptedkey unwrap to the key; honey tooling that calls
// stack.FormatSecureRef in tests).
// Dry-run shows <<secret …>> placeholders; --execute decrypts on the machine running honey.
//
//   honey cue-exec examples/recipe/with_secrets.cue my-filter
//   honey cue-exec --execute examples/recipe/with_secrets.cue my-filter
recipe: {
	name: "with-secrets"
	defaults: {
		// Replace with a real secure:v1 blob for your stack data key (placeholder will fail decrypt).
		secrets: {DEMO_FROM_DEFAULTS: "secure:v1:AAAAAAAAAAAAAAAA:YmFj"}
	}
	steps: [
		{
			host:    "*"
			command: "printenv DEMO_FROM_DEFAULTS DEMO_STEP_ONLY | wc -c"
			secrets: {DEMO_STEP_ONLY: "secure:v1:AAAAAAAAAAAAAAAA:YmFj"}
		},
	]
}
