# Tutor Output Review Failure Contract

- Timeout or context cancellation: return no approval and fail closed.
- HTTP 429 or 5xx: use only the bounded reviewer retry policy; exhaustion remains fail closed.
- Other HTTP errors: do not retry and do not approve the candidate.
- Invalid local JSON Schema output: classify as invalid schema and fail closed.
- Provider, model, or request ID source mismatch: classify as invalid provenance and fail closed.
- PASS/REJECT fields or violation entries that disagree: classify as inconsistent violations and fail closed.
- Audit persistence failure: do not release the candidate to storage, WebSocket, or TTS.
