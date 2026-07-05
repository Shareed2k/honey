# Email Notifications Design Specification

## Overview
Add Dagu-style rich HTML email notifications to `hostctl` for recipe executions. Notifications will be triggered at the end of a recipe run based on the overall success or failure status. The SMTP transport will be configured globally via the main `honey.yaml` configuration, while routing (recipients, prefix, success/failure triggers) will be defined explicitly per-recipe in the CUE schema.

## Architecture

We will implement a standalone `Reporter` module inside `internal/engine` that mirrors Dagu's approach to rendering and transmitting HTML summaries. The core `RecipeRunner` will invoke this reporter synchronously at the end of a run.

### 1. Global SMTP Configuration
A new `SMTP` block will be added to the `config.File` struct (in `internal/config`), loaded from `honey.yaml`:

```yaml
smtp:
  host: smtp.example.com
  port: 587
  username: alerts@example.com
  password: secure-password
```

### 2. CUE Schema Overrides
The `#Recipe` schema in `internal/cuetry/recipe.go` will be extended to accept recipe-level mail routing parameters:

```cue
#Recipe: close({
    // ... existing ...
    mail_on?: close({
        success?: bool
        failure?: bool
    })
    error_mail?: close({
        from: string
        to: [...string]
        prefix?: string
        attach_logs?: bool
    })
    info_mail?: close({
        from: string
        to: [...string]
        prefix?: string
    })
})
```

### 3. Engine Integration (The Reporter)
A new `internal/engine/reporter.go` file will define the `Reporter` interface and its logic:
- The reporter will inspect the `Recipe.MailOn` configuration at the end of the run.
- If the run failed and `mail_on.failure` is true, it uses the `error_mail` config.
- If the run succeeded and `mail_on.success` is true, it uses the `info_mail` config.
- The reporter initializes a standard Go `net/smtp` client (or utilizes `github.com/nikoksr/notify/service/mail` purely as a direct library implementation) with the global SMTP credentials.
- It generates an HTML table containing the overall Run Status, Start/Finish times, and a breakdown of individual step outcomes and errors.
- If `attach_logs` is true, it extracts the collected `stdout`/`stderr` from the `SessionRecorder` or `HostExecResult` arrays and attaches them as text files.

## Error Handling
- Invalid SMTP configurations will disable the reporter silently (or with a warning log) rather than crashing the engine.
- Mail transmission failures will be logged as warnings but will **not** affect the actual execution status of the recipe run.

## Testing
- Unit tests for the CUE schema validation ensuring `mail_on`, `error_mail`, and `info_mail` parse correctly.
- Tests for the `Reporter`'s HTML rendering and condition evaluation logic.
- The `Reporter` will accept a mockable `Sender` interface to verify payload generation without executing real SMTP network requests.