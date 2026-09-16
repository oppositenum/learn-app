# Qwen Structured Output Compatibility Evidence

## Evidence Boundary

- Evidence status: `UNVERIFIED`
- Real-provider sample count: `0`
- Time window: not started
- Model: not configured
- Region: not configured
- Direction samples: Qwen generation / Doubao review `0`; Doubao generation / Qwen review `0`

No effective `ai_price_catalog` row exists for a Qwen model, so **the harness has not run and every check below is still `UNVERIFIED`**. Public documentation and OpenAI-gateway evidence are not treated as Qwen evidence.

A separate manual smoke observation on 2026-09-16 did reach the provider. It is recorded under "Manual Smoke Observation" below and is a weaker evidence class than a harness run: it carried no price preflight, wrote no usage record, and sampled each cell once. It cannot close any check in the table.

## Required Checks

| ID | Check | Real-provider status | Current evidence |
| --- | --- | --- | --- |
| a | Endpoint availability; Responses `text.format.json_schema`; Chat Completions `response_format` | `UNVERIFIED` | The repeatable harness builds both request shapes. Mock tests only verify request construction. |
| b | Direct strict acceptance of the three runtime schemas | `UNVERIFIED` | The harness sends each schema verbatim through each accepted request shape. No real request ran. |
| c | Every keyword currently blocked in `compatibility.go` | `UNVERIFIED` | The harness obtains the keyword list at runtime and probes each keyword separately. The existing OpenAI/gateway list was not copied as a Qwen conclusion. |
| d | Enforcement, ignore, or rejection of `additionalProperties:false`, `required`, `enum`, `minLength`, `maxLength`, `minimum`, `maximum` | `UNVERIFIED` | Local classification code distinguishes provider rejection, schema-valid output, and accepted schema-invalid output. Mock tests cover classifier behavior only. |
| e | Usage and cached-token field paths | `UNVERIFIED` | The harness records numeric token paths and cache-related token paths without retaining response text. No Qwen response was observed. |
| f | Response `model` versus configured model/endpoint ID | `UNVERIFIED` | The harness records both strings and equality. Production accounting remains keyed to the configured provider/model pair. |
| g | `previous_response_id` or equivalent conversation linkage | `UNVERIFIED` | The harness performs a second Responses request with the actual first response ID or a Chat request with prior assistant content. No real request ran. |
| h | Error HTTP status, error-code paths, and `Retry-After` | `UNVERIFIED` | The harness sends a deliberately invalid schema and records only bounded metadata paths, never the provider body. |
| i | Context cancellation propagation and prompt connection return | `UNVERIFIED` | The harness cancels an in-flight request and records whether the client returns within five seconds. No real connection was opened. |
| j | Generation/review P50, P95, P99 and both end-to-end directions against the 75-second budget | `UNVERIFIED` | The harness supports configurable sample counts, bounded 429/5xx retries, price preflight, usage recording, and both provider directions. No timing sample ran. |

| k | Reasoning/thinking control: which request parameter disables it, and what it is worth against the 75-second budget | `UNVERIFIED` | The harness applies a configurable `PROVIDER_PROBE_QWEN_REASONING_CONTROL` overlay, records the applied value in every observation and in the report, and samples a small reasoning-on comparison series so latency is never reported without stating which control produced it. |

## Manual Smoke Observation

Not harness evidence. Manual `curl`, 2026-09-16, one sample per cell, no price preflight, no usage record, model `qwen3.7-plus-2026-05-26`, region `cn-beijing`.

| Observed | Result of that run |
| --- | --- |
| Responses `text.format.json_schema` | HTTP 200, but the schema was **silently ignored**: the returned object used invented keys (`context`, `guiding_question`, `tone`) and none of the required `tutor_turn` fields |
| Chat Completions `response_format.json_schema` | HTTP 200; output satisfied `tutor_turn.schema.json` under `santhosh-tekuri/jsonschema` v6.0.3 |
| Provider default reasoning | 55.5s via Chat Completions; 18.3s via Responses |
| `{"enable_thinking":false}` | 3.9s; output still schema-valid |
| `maxLength: 1200` under default reasoning | Enforced at exactly 1200 characters, but the model filled the allowance with a repeating emoji sequence; with reasoning disabled the same field was 58 characters |
| All three runtime schemas via Chat Completions | Accepted in that run |
| Response `model` | Returned the configured pinned snapshot string unchanged |

The silently-ignored Responses schema is the reason check `b` cannot be closed by a status code: an accepted request is not an enforced schema. These are statements about that run only, not a claim that Qwen supports or does not support any of the above.

## Mock Evidence

`server/cmd/provider-compat-probe/probe_test.go` proves only local harness behavior:

- Responses and Chat Completions request payloads contain strict JSON Schema controls.
- Missing price prevents all provider network traffic.
- Successful responses are accounted under the configured provider/model identity even when the response model differs.
- Accounting failure is never classified as accepted output.
- Constraint classification distinguishes locally schema-valid and schema-invalid output.

These tests are `MOCK` evidence. They do not establish any Qwen capability.

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

Optional non-secret controls are `PROVIDER_PROBE_SAMPLES`, `PROVIDER_PROBE_TIMEOUT_SECONDS`, `PROVIDER_PROBE_OUTPUT_DIR`, per-provider auth header/prefix settings, and `PROVIDER_PROBE_<NAME>_REASONING_CONTROL`. The reasoning control is a JSON object merged into the top level of every request; it may not override `model`, `messages`, `input`, `instructions`, `response_format`, `text`, or `previous_response_id`. Set it to `none` to sample the provider's own default instead. Its default value is a convenience based on the 2026-09-16 manual observation, not a claim about the provider. Before execution, both configured provider/model pairs must have effective `ai_price_catalog` rows. Run from the repository root:

```sh
PROVIDER_PROBE_CONFIRM_LIVE=1 go run ./server/cmd/provider-compat-probe
```

The generated report is written with mode `0600` below ignored `tmp/provider-compat` by default. It contains capability metadata and latency distributions, not keys, prompts returned by models, private answers, or raw response bodies.

## Provider-Specific Decision

No Qwen-specific blocked-keyword list, preferred endpoint shape, usage mapping, model-identity rule, retry conclusion, latency conclusion, or generator/reviewer assignment is approved. All remain `UNVERIFIED` pending a sufficiently sized real-provider run.
