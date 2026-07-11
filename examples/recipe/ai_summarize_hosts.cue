// Remote recipe: sample hosts then one terminal local summary (OpenAI-compatible API).
//
// The final `summarize` step must be last, with host: "_". It runs in the honey process (not over SSH),
// after all prior steps complete, sending their combined output to the model in one request.
//
// Prerequisites for --execute on the summarize step:
//   OPENAI_API_KEY (required)
//   OPENAI_BASE_URL (optional; default OpenAI API)
//   OPENAI_MODEL (optional if summarize.model omitted)
//
// Optional honey YAML defaults.ai_system_prompt overrides the built-in system prompt; optional
// recipe summarize.system_prompt overrides both.
//
// Optional notify (after successful LLM): step-level `notify` block (presence enables it), e.g.
//   notify: {}
//   notify: { notify_subject: "..." , message: "custom notify body" }
//   notify: { services: { slack: { channel_id: "C..." }, telegram: {} } }  // allowlist; webhook URL still from env
// Receivers: HONEY_NOTIFY_HTTP_URL, HONEY_NOTIFY_SLACK_WEBHOOK_URL, HONEY_NOTIFY_TELEGRAM_BOT_TOKEN + CHAT_IDS
//
// Validate:
//   honey cue-validate examples/recipe/ai_summarize_hosts.cue
recipe: {
	name: "ai-summarize-hosts-example"

	steps: [
		{
			host: "*"
			command: "echo \"=== $HONEY_HOST_NAME ($HONEY_HOST_PRIMARY_IP) ===\" && hostname"
		},
		{
			host: "_"
			// notify: {}
			// notify: { notify_subject: "...", message: "...", services: { slack: {}, telegram: {} } }
			summarize: {
				prompt: """
Summarize the host listing in 3–5 bullet points. Note any missing output or failures.
"""
				// model: "gpt-4o-mini"
				// max_input_chars: 100000
				// max_output_tokens: 800
			}
		},
	]
}
