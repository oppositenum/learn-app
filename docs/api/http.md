# HTTP API Contract

All protected routes accept an HttpOnly same-origin `session_token` cookie. Bearer tokens are supported for non-browser clients and automated tests. JSON errors use an HTTP status and a short non-sensitive message. Student responses never serialize private answer fields.

## Identity

| Method | Path | Response | Security |
|---|---|---|---|
| `POST` | `/api/v1/auth/login` | Current user role and display identity | Email/password; generic failure response and attempt lockout |
| `GET` | `/api/v1/auth/me` | Current user role, display name, optional Student ID | Authenticated session only |
| `POST` | `/api/v1/auth/logout` | `204 No Content` | Revokes the server-side session and expires the cookie |

Browser credentials are held only in a `HttpOnly`, `SameSite=Strict` cookie. The database stores a SHA-256 token hash, not the session token. Initial Owner accounts can be created through the Owner-controlled `server/cmd/provision` command described in the README; authenticated Owners manage Student and Parent accounts through the Owner account APIs below.

## Student

| Method | Path | Response | Security |
|---|---|---|---|
| `GET` | `/api/v1/student/questions/{id}` | Released `QuestionPublic` | Student only; `DRAFT`/`QUARANTINED` are 404 |
| `GET` | `/api/v1/student/today` | Proposed/active plans and blocks | Student's own record only |
| `POST` | `/api/v1/student/sessions` | Starts or resumes the released question selected for a current plan block | Student's own current-day block only |
| `GET` | `/api/v1/student/sessions/current` | Current active session or `204 No Content` | Student's own record only |
| `GET` | `/api/v1/student/sessions/{session_id}` | Public classroom prompt and public timeline | Student must own session; no private answer fields |
| `POST` | `/api/v1/student/sessions/{session_id}/answers` | Tutor action, Socratic round, safe message, optional voice audio/segments, safe mastery/reward summary | Student must own active session; no answer echo |
| `POST` | `/api/v1/student/sessions/{session_id}/voice/complete` | Explicitly transitions `VOICE_EXPLAIN` to `RETURN` while keeping the released original question active | Student must own active session; Parent is forbidden |
| `POST` | `/api/v1/student/sessions/{session_id}/reflection` | Saves `CONTINUE_TOMORROW`, `PAUSE`, or `STOP` after a completed classroom | Student must own the completed session; Parent/Owner cannot submit |
| `POST` | `/api/v1/student/sessions/{session_id}/support` | Safe `HINT` or parallel-example `EXPLAIN` Tutor turn | Student must own active session; strict body; no answer/analysis row and no failed-round increment |
| `GET` | `/api/v1/student/growth` | Energy, streak, growth-base JSON | Student's own record only |
| `POST` | `/api/v1/student/speech/transcriptions` | Transcript requiring confirmation; raw-audio deletion status | Student only; multipart `audio`, `duration_seconds`, optional `session_id` |

Example safe answer response:

```json
{"session_id":"uuid","action":"SCAFFOLD","socratic_round":2,"message":"先只完成最小的一步：圈出题目要求你判断或求出的对象。"}
```

Support request bodies are exactly `{"type":"HINT"}` or `{"type":"EXPLAIN"}`. Unknown fields, unsupported types, and trailing JSON are rejected. Generated support is validated against the server-authorized action and answer non-disclosure schema before a Tutor turn is persisted.

While a session is in `VOICE_EXPLAIN`, direct answer submission returns `409 Conflict`. The Student must complete the voice explanation first; the server then records `VOICE_EXPLAIN_COMPLETED`, enters `RETURN`, and requires a fresh answer to the same released question.

`GET` session responses in `VOICE_EXPLAIN` include the persisted safe TTS `voice_audio` data URL and synchronized `voice_segments`, so refreshing the voice page does not lose the explanation or block `RETURN`. The Student supply route is likewise session-bound (`/student/session/{session_id}/supply`) and reloads its safe classroom timeline instead of depending on browser memory. Raw child STT audio is not persisted.

## Parent

| Method | Path | Response | Security |
|---|---|---|---|
| `GET` | `/api/v1/parent/children` | Bound children and optional active-session discovery | Parent only; active links only |
| `GET` | `/api/v1/parent/child/{student_id}/session/{session_id}` | Public question plus private supervision analysis/answer | Bound Parent only |
| `GET` | `/api/v1/parent/child/{student_id}/ability` | Subject mastery, core-ability evidence, and active misconceptions | Bound Parent only; read-only and answer-free |
| `GET` | `/api/v1/parent/child/{student_id}/report` | Activity, rewards, and recent classroom summaries | Bound Parent only; read-only and answer-free |
| `GET` | `/api/v1/parent/child/{student_id}/preferences` | Effective plan preferences including `enabled_subject_codes` and `configured` | Bound Parent only |
| `PUT` | `/api/v1/parent/child/{student_id}/preferences` | Save confirmation and `answer_controls_available=false` | Bound Parent only; 15-60 minutes |
| `POST` | `/api/v1/parent/child/{student_id}/interventions` | Saves one fixed lightweight intervention | Bound Parent only; unknown fields rejected |

Allowed preference fields are `daily_minutes`, `priority_subject_codes`, `review_only`, `reduce_intensity`, and `enabled_subject_codes`. `enabled_subject_codes` must be a subset of `MATH`/`CHINESE`/`ENGLISH`/`PHYSICS`/`CHEMISTRY` with at least one entry when provided; omitting it (or the stored empty array) means all subjects are enabled. The daily plan contains one block per enabled subject with released content, splitting `daily_minutes` evenly. There is no send-answer or per-question control endpoint.

Allowed intervention types are `ENCOURAGEMENT`, `REDUCE_INTENSITY`, `REVIEW_ONLY`, and `STATE_NOT_GOOD`. The server authors the child-facing message. An `answer` field is invalid, and no answer-delivery route exists.

## Owner

| Method | Path | Response | Security |
|---|---|---|---|
| `GET` | `/api/v1/owner/content` | Content status, version, validation/review evidence | Owner only |
| `GET` | `/api/v1/owner/accounts` | Student and Parent identities plus Parent-Student links; never credentials | Owner only |
| `POST` | `/api/v1/owner/accounts/students` | Creates a Student identity, credential, and grade profile atomically | Owner only; password is write-only |
| `POST` | `/api/v1/owner/accounts/parents` | Creates a Parent identity, credential, and one or more Student links atomically | Owner only; password is write-only |
| `POST` | `/api/v1/owner/accounts/links` | Creates or reactivates a Parent-Student supervision link | Owner only |
| `POST` | `/api/v1/owner/content/drafts` | Imports a schema-valid DRAFT with generator provenance | Owner only |
| `GET` | `/api/v1/owner/content/generation-options` | Lists released, source-linked knowledge points with subject, grade band, domain, unit, stable code, provenance, licensed question sources, and generator availability | Owner only; no private answer data |
| `POST` | `/api/v1/owner/content/generate` | Generates 1-5 original questions and atomically persists server-owned DRAFT assets | Owner only; records priced `CONTENT_GENERATION` usage and cannot publish |
| `POST` | `/api/v1/owner/content/{question_id}/validate` | Runs deterministic checks and persists their evidence | Owner only; DRAFT state required |
| `POST` | `/api/v1/owner/content/{question_id}/review` | Runs configured independent structured review | Owner only; validated state and distinct reviewer required |
| `POST` | `/api/v1/owner/content/{question_id}/release` | Releases using server-looked-up PASS evidence | Owner only; reviewed state required |
| `POST` | `/api/v1/owner/content/{question_id}/quarantine` | Immediately removes released content from classroom selection | Owner only; reason required |
| `GET` | `/api/v1/owner/costs` | Daily model/purpose/token/audio/cost aggregates and normalized summary metrics | Owner only |
| `GET` | `/api/v1/owner/trials` | Longest/current activity and child-submitted willingness streaks | Owner read-only; cannot create reflections |

Cost filters may be combined: `student_id`, `session_id`, `subject`, `date_from`, `date_to`, `model`, and `purpose`. Summary fields are `total_cost_usd`, `cost_per_active_student_day_usd`, `cost_per_20_minute_lesson_usd`, `cost_per_mastered_skill_usd`, `cached_ratio`, `stt_cost_usd`, `tts_cost_usd`, `strong_model_ratio`, and `average_tokens_per_request`.

The 15 curated seed questions retain explicit curated validation/review provenance. Migration `000013_seed_asset_evidence.sql` reconstructs their complete DRAFT assets, and the PostgreSQL integration suite runs every asset through the same deterministic Validator used by the Owner HTTP pipeline. This does not retroactively claim that migration `000007` invoked the Go validator or a real AI reviewer.

The generation catalog contains the 132 assessable entries explicitly listed in the V1 product specification plus 10 preserved Task 019 detail points. `初三模块预留` is intentionally not runtime-selectable. The catalog source is identified as an internal product specification and must not be represented as an official national curriculum standard.

`GET /healthz` is public and does not expose database or provider secrets.
