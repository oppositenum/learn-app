# Doubao Structured Output Compatibility Evidence

## Evidence Boundary

- Evidence status: `HARNESS VERIFIED (small sample)`
- Harness run: `2026-09-16T11:44:01.084902Z` to `2026-09-16T11:45:46.915781Z`, `PROVIDER_PROBE_SAMPLES=5`
- Model: `doubao-seed-2-1-pro-260628` (pinned snapshot)
- Region: `cn-beijing`
- Reasoning control applied to every call: `{"thinking":{"type":"disabled"}}`
- Latency sample count: generation `5`, review `5`, reasoning-on comparison `3`
- Direction samples: Doubao generation / Qwen review `5`; Qwen generation / Doubao review `5`

Every request below passed `ai_price_catalog` preflight and wrote an `ai_usage_records` row, so these are accounted real-provider observations. They are still a **small sample from a single session on one account**: they describe what that run observed and are not a general claim about what Doubao supports. Percentile figures at `n=5` are indicative only. Public documentation and OpenAI-gateway evidence are not treated as Doubao evidence.

## Preferred Request Shape

`chat_completions` — chat_completions enforced 5 of 7 probed schema constraints.

Both shapes returned accounted `200` responses, so an accepted status code could not be used to choose between them. The shape was selected on observed constraint enforcement instead.

## Required Checks

| ID | Check | Status | Observation |
| --- | --- | --- | --- |
| a | Endpoint availability and structured-output shape | `OBSERVED` | Both `responses` and `chat_completions` returned accounted `200`. |
| b | Strict acceptance of the three runtime schemas | `OBSERVED` | `analyze_answer`, `tutor_turn`, and `tutor_output_review` were accepted verbatim on both shapes. Acceptance is not enforcement; see check `d`. |
| c | Keywords currently blocked in `compatibility.go` | `OBSERVED` | Rejected: none. Accepted: `allOf`, `anyOf`, `dependencies`, `dependentRequired`, `dependentSchemas`, `else`, `if`, `not`, `oneOf`, `patternProperties`, `then`. Accepted here means the request completed, not that the keyword changed the output. The OpenAI list was not reused. |
| d | Enforcement of schema constraints | `OBSERVED` | See the table below. |
| e | Usage and cached-token field paths | `OBSERVED` | `$.usage.completion_tokens`, `$.usage.completion_tokens_details.reasoning_tokens`, `$.usage.prompt_tokens`, `$.usage.prompt_tokens_details.cached_tokens`, `$.usage.total_tokens`. Cached tokens at `$.usage.prompt_tokens_details.cached_tokens`. |
| f | Response `model` versus configured model | `OBSERVED` | Returned `doubao-seed-2-1-pro-260628`, matching the configured pinned snapshot. Accounting stays keyed to the configured pair regardless. |
| g | Conversation linkage | `OBSERVED` | `ACCEPTED_ACCOUNTED` on `chat_completions`. |
| h | Error status, error-code paths, `Retry-After` | `NOT PRODUCED` | A deliberately invalid schema was accepted with `200` rather than rejected, so no error shape was produced. No 429/5xx was observed in this run, so retry handling remains unmeasured. |
| i | Cancellation propagation | `OBSERVED` | `CANCEL_PROPAGATED`: an in-flight request returned within five seconds of cancellation. |
| j | Latency against the 75-second budget | `OBSERVED (n=5)` | Generation p50 `1458ms` / p99 `1739ms`; review p50 `924ms` / p99 `1411ms`. End to end: Doubao→Qwen p99 `4989ms`, Qwen→Doubao p99 `4198ms`. Both directions: `OBSERVED_SAMPLES_WITHIN_75_SECONDS`. |
| k | Reasoning control and its worth | `OBSERVED` | With `{"thinking":{"type":"disabled"}}` generation p50 was `1458ms`. With the control removed, p50 was `6560ms` and p99 `11330ms` over 3 samples. Latency in this document is only valid with the control applied. |

## Constraint Enforcement

Each constraint was probed by sending a schema that forbids a value and a prompt that explicitly asks for that value. A compliant output means the provider constrained generation; a violating output means it did not. The verdicts are asymmetric on purpose: under real constrained decoding a violation cannot occur, so one violating sample settles `IGNORED`, while `ENFORCED` requires all 3 attempts to comply.

| Constraint | `responses` | `chat_completions` |
| --- | --- | --- |
| `additionalProperties` | `ENFORCED` (3 attempt(s)) | `ENFORCED` (3 attempt(s)) |
| `required` | `IGNORED` (1 attempt(s)) | `ENFORCED` (3 attempt(s)) |
| `enum` | `ENFORCED` (3 attempt(s)) | `ENFORCED` (3 attempt(s)) |
| `minLength` | `IGNORED` (3 attempt(s)) | `ENFORCED` (3 attempt(s)) |
| `maxLength` | `IGNORED` (1 attempt(s)) | `IGNORED` (1 attempt(s)) |
| `minimum` | `IGNORED` (2 attempt(s)) | `IGNORED` (2 attempt(s)) |
| `maximum` | `ENFORCED` (3 attempt(s)) | `ENFORCED` (3 attempt(s)) |

## Impact On The Runtime Schemas

- `enum` is enforced on `chat_completions`, so the server-pinned Tutor action in `constrainActionEnum` is honoured by the provider on that shape.
- Any constraint marked `IGNORED` is not enforced by the provider. Local `santhosh-tekuri/jsonschema` validation still rejects violating output, and a local schema failure is currently **not** retryable, so an ignored constraint turns into a hard generation failure rather than a safety hole.
- No change to the runtime schemas, the local validator, or the retry classification is approved on this evidence. Widening retries or relaxing a schema is a separate proposal.

## Mock Evidence

`server/cmd/provider-compat-probe/probe_test.go` proves only local harness behavior:

- Both request shapes carry strict JSON Schema controls, and the reasoning control cannot overwrite them.
- Missing price prevents all provider network traffic.
- Successful responses are accounted under the configured provider/model identity even when the response model differs.
- Accounting failure is never classified as accepted output.
- Constraint classification distinguishes enforced from ignored, and `ENFORCED` requires every attempt to comply.
- The preferred shape is chosen on enforcement, not on an accounted status code.

These tests are `MOCK` evidence. They do not establish any Doubao capability.

## Repeatable Live Procedure

The command refuses to make network calls unless `PROVIDER_PROBE_CONFIRM_LIVE=1` is explicitly set. It additionally requires `TEST_DATABASE_URL`; every request performs price preflight and records either usage or a bounded request outcome.

Required server-side settings are:

```text
PROVIDER_PROBE_DOUBAO_PROVIDER
PROVIDER_PROBE_DOUBAO_BASE_URL
PROVIDER_PROBE_DOUBAO_API_KEY
PROVIDER_PROBE_DOUBAO_MODEL
PROVIDER_PROBE_DOUBAO_REGION
PROVIDER_PROBE_QWEN_PROVIDER
PROVIDER_PROBE_QWEN_BASE_URL
PROVIDER_PROBE_QWEN_API_KEY
PROVIDER_PROBE_QWEN_MODEL
PROVIDER_PROBE_QWEN_REGION
TEST_DATABASE_URL
```

Optional non-secret controls are `PROVIDER_PROBE_SAMPLES`, `PROVIDER_PROBE_TIMEOUT_SECONDS`, `PROVIDER_PROBE_OUTPUT_DIR`, per-provider auth header/prefix settings, and `PROVIDER_PROBE_<NAME>_REASONING_CONTROL`. The reasoning control is a JSON object merged into the top level of every request; it may not override `model`, `messages`, `input`, `instructions`, `response_format`, `text`, or `previous_response_id`. Set it to `none` to sample the provider's own default instead. Before execution, both configured provider/model pairs must have effective `ai_price_catalog` rows. Run from the repository root:

```sh
PROVIDER_PROBE_CONFIRM_LIVE=1 go run ./server/cmd/provider-compat-probe
```

The generated report is written with mode `0600` below ignored `tmp/provider-compat` by default. It contains capability metadata and latency distributions, not keys, prompts returned by models, private answers, or raw response bodies.

## Not Yet Established

- No generator/reviewer assignment between Doubao and Qwen is approved. Both directions completed inside the budget at `n=5`; that does not decide which provider should generate and which should review.
- No 429 or 5xx was observed, so retry and `Retry-After` behavior is unmeasured.
- No sustained-load, multi-session, or multi-day sample exists. `n=5` percentiles must not be quoted as production latency.
- No real child usage evidence exists, and nothing here has been deployed to the test server.
