# Tutor Generation Failure Contract

- Timeout or context cancellation: stop work, return no candidate output, and preserve cancellation.
- HTTP 429 or 5xx: apply the bounded Tutor retry policy; exhaustion returns the stable busy failure.
- Other HTTP errors: do not retry and do not publish a candidate.
- Invalid local JSON Schema output: reject immediately; do not retry or pass partial data to classroom rules.
- Accounting failure: treat the request as failed even when the provider returned output.
- Audit source mismatch or audit unavailability: fail closed; the generated candidate must not be persisted, published, or sent to TTS.
- Audit rejection or an inconsistent violation verdict: fail closed and require rephrasing; never expose the rejected candidate.
