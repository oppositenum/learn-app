# Tutor Provider Identity Baseline

## Baseline

- Branch: `origin/feat/v1-roadmap-completion`
- Commit: `03f4cc987d1cf229aba961dcfafaed4078aabe71`
- Working branch: `feat/provider-identity-and-prompts`
- Scope: runtime Tutor structured generation and independent Tutor-output review only. Content pipeline and speech providers are out of scope.

The baseline commit has a known failing GitHub Actions PostgreSQL integration job caused by a low-frequency classroom submission timing failure. This document records that condition without attributing it to provider work or claiming a fix.

## A-H Verification

### A. Provider integration layer: MATCH

`TeachingAgent` declares the five classroom methods in `server/internal/ai/gateway.go:119`. The sole runtime implementation, `CodexProvider`, depends on the one-method `StructuredClient` interface in `server/internal/ai/codex_provider.go:21`. Provider-specific transport therefore belongs below `TeachingAgent`, at the `StructuredClient` boundary. No new `TeachingAgent` implementation is required by this task.

### B. Hard-coded provider identity: MATCH

`server/internal/ai/responses_client.go` hard-codes `openai` at all three stated points:

- line 155: price preflight;
- line 184: successful `ModelUsage`;
- line 223: request-outcome recording.

### C. Price-key inconsistency: MATCH

Price preflight uses configured `client.model` at `server/internal/ai/responses_client.go:155`. Successful usage uses response `decoded.Model` at line 184. `server/internal/usage/recorder.go:61` then queries `ai_price_catalog` with `record.Usage.Provider` and `record.Usage.Model`. An endpoint ID configured as the request model can therefore pass preflight, incur a network request, and fail accounting if the response reports a different model name.

The required rule for P1 is: the configured provider and configured model form the canonical accounting identity. The response model is transport metadata, not a price-catalog key. Price preflight, `ModelUsage`, `ai_usage_records`, `ai_request_outcomes`, and audit provenance must all use that canonical pair.

### D. Reviewer provenance dependence: MATCH

`server/internal/tutoraudit/openai_reviewer.go:103` replaces review evidence provider/model with `StructuredResult.Usage`. `server/internal/tutoraudit/service.go:119` compares that effective identity with the configured reviewer identity and fails closed with `INVALID_PROVENANCE` when it differs. Provider/model propagation must therefore use the same canonical configured identity used by accounting.

### E. Inline prompts: MATCH

Generation instructions are inline in `server/internal/ai/codex_provider.go:118`, lines 158-170, and constants at lines 173-175. Review instructions are the inline `reviewerInstructions` constant in `server/internal/tutoraudit/openai_reviewer.go:20`. They are distinct text today, but are neither versioned assets nor accompanied by committed examples and failure contracts.

### F. Strict schemas and compatibility evidence: MATCH

The three runtime schemas are `schemas/ai_outputs/analyze_answer.schema.json`, `tutor_turn.schema.json`, and `tutor_output_review.schema.json`. They use strict required fields, enumerations, and `additionalProperties: false`; the generation schemas also contain the numeric and string constraints stated in the task. `schemas/ai_outputs/compatibility.go:18` records gateway-specific evidence for blocked keywords. That evidence cannot be reused as Doubao or Qwen evidence.

### G. Retry and timeout behavior: MATCH

`server/internal/ai/retry_policy.go:6` defines three attempts, 2-second base delay, 1-second maximum jitter, 20-second aggregate retry wait, and a 60-second retry timeout. `server/internal/ai/codex_provider.go:19` defines the 85-second generation-plus-audit operation timeout. `server/internal/classroom/lifecycle.go:21` preserves the `75s < 85s < 90s` classroom/proxy/stale-session ordering. `IsRetryableResponsesError` in `server/internal/ai/responses_client.go:38` retries only HTTP 429 and 5xx responses; local schema failures are not retryable.

### H. Single-channel composition root: MATCH

`server/cmd/api/main.go:90` configures both Tutor generation and review from `OPENAI_API_KEY` and `OPENAI_BASE_URL`. Lines 92-110 create two clients but hard-code both identities as `openai:<model>`. The content-pipeline clients at lines 130-150 and speech setup at lines 73-87 are separate, explicitly out-of-scope paths.

## Identity And Accounting Flow

```text
environment configuration
  OPENAI_API_KEY / OPENAI_BASE_URL / configured model
          |
          v
server/cmd/api composition root
  generator identity = openai:<configured tutor model>
  reviewer identity  = openai:<configured reviewer model>
          |
          v
OpenAIResponsesClient (StructuredClient)
  configured client.model
          |
          +--> PriceGuard.EnsurePrice(openai, configured model, request start)
          |      |
          |      +--> ai_price_catalog(provider, model, effective interval)
          |      +--> missing price: fail closed before HTTP
          |
          +--> POST /responses model=<configured model>
          |      |
          |      +--> response id, response model, output JSON, usage counters
          |
          +--> ModelUsage
          |      baseline: provider=openai, model=<response model>
          |      P1 rule: provider/model=<canonical configured identity>
          |
          +--> UsageRecorder.RecordAIUsage
          |      +--> price lookup by ModelUsage provider/model
          |      +--> ai_usage_records + price_catalog_id
          |
          +--> RequestOutcomeRecorder
          |      +--> ai_request_outcomes using configured provider/model
          |
          v
Tutor reviewer evidence
  OpenAIReviewer replaces evidence provider/model with ModelUsage
          |
          v
Tutor audit service
  compare evidence provider:model with configured reviewer identity
          |
          +--> mismatch: INVALID_PROVENANCE, fail closed
          +--> match: persist tutor_output_audits and allow remaining gates
```

## Hard-coded Provider Strings

Runtime Tutor and shared structured-client path:

- `server/internal/ai/responses_client.go:155` - price guard provider `openai`.
- `server/internal/ai/responses_client.go:184` - successful usage provider `openai`.
- `server/internal/ai/responses_client.go:223` - request-outcome provider `openai`.
- `server/cmd/api/main.go:100` - Tutor reviewer constructor provider `openai`.
- `server/cmd/api/main.go:109` - Tutor generator identity prefix `openai:`.
- `server/cmd/api/main.go:110` - Tutor reviewer identity prefix `openai:`.

Known hard-coded strings outside this task's implementation scope:

- `server/cmd/api/main.go:134` and line 138 - content generator provider and identity.
- `server/cmd/api/main.go:146` and line 150 - content reviewer provider and identity.
- `server/internal/speech/openai_provider.go` - OpenAI-specific speech adapter and API routes.

Test fixtures also contain deliberate `openai` identities. They are compatibility assertions or synthetic provenance fixtures, not production provider selection.
