# Qwen Structured Output Compatibility Evidence

## Evidence Boundary

- Evidence status: `UNVERIFIED`
- Real-provider sample count: `0`
- Time window: not started
- Model: not configured
- Region: not configured
- Direction samples: Qwen generation / Doubao review `0`; Doubao generation / Qwen review `0`

No Qwen credential, endpoint, model, region, real PostgreSQL test connection, or effective price row was available in the execution environment. No network request was sent. Public documentation and OpenAI-gateway evidence are not treated as Qwen evidence.

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

Optional non-secret controls are `PROVIDER_PROBE_SAMPLES`, `PROVIDER_PROBE_OUTPUT_DIR`, and per-provider auth header/prefix settings. Before execution, both configured provider/model pairs must have effective `ai_price_catalog` rows. Run from the repository root:

```sh
PROVIDER_PROBE_CONFIRM_LIVE=1 go run ./server/cmd/provider-compat-probe
```

The generated report is written with mode `0600` below ignored `tmp/provider-compat` by default. It contains capability metadata and latency distributions, not keys, prompts returned by models, private answers, or raw response bodies.

## Provider-Specific Decision

No Qwen-specific blocked-keyword list, preferred endpoint shape, usage mapping, model-identity rule, retry conclusion, latency conclusion, or generator/reviewer assignment is approved. All remain `UNVERIFIED` pending a sufficiently sized real-provider run.
