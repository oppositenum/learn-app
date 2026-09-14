# AI Learning Tutor

AI Learning Tutor is a mobile-first, five-subject Socratic learning prototype for primary and junior-secondary students. The V1.1 revision of [`docs/product/互动式学习_V1.md`](docs/product/%E4%BA%92%E5%8A%A8%E5%BC%8F%E5%AD%A6%E4%B9%A0_V1.md) is the sole current product and acceptance baseline; it supersedes the original V1 frozen baseline preserved at commit `a009be5d43c8edcfb6190a2c8a3967b04cf2758f`. Non-negotiable engineering rules are in [`AGENTS.md`](AGENTS.md).

## Implemented V1 Surface

- Student, Parent, and Owner authorization with explicit Parent-Student binding. Parent live views receive only a server-bounded current short-answer preview; completed reports contain evidence and safety summaries without verbatim Student or Tutor dialogue.
- PostgreSQL-level public/private answer separation and authenticated Student non-disclosure tests.
- Five-subject curriculum with all 132 assessable primary/junior-secondary skeleton entries from the V1 product specification, 10 preserved high-quality detail points, prerequisite graph, cross-subject graph, abilities, and misconceptions. The 142 released points are organized into 44 useful subject/grade-band domains rather than the original broad three-domain demo taxonomy.
- The deployment branch contains a server-authoritative `ORIGINAL -> VARIANT -> ABSTRACT -> VERIFY -> COMPLETE` flow for complete `READY` task lineages. Every stage uses a versioned deterministic scorer, excludes every task already presented in the session, and requires a fresh independent task after help. The auditable `seed-linear-equation` loader activates one four-stage `MATH-LINEAR-EQUATION` lineage (12 released structured tasks), covering 1 of 142 released knowledge points; the other 141 remain on the legacy classroom path. Misconception taxonomy links currently cover 15 of 142 released knowledge points, leaving 127 with zero associations; content for those points must use an empty misconception list to pass the fail-closed Stage 3 gate, so diagnostic and ReviewQueue coverage is limited. Databases where the loader has not been run remain fail-closed with zero READY lineages. B3, B4, B5, and B6 engineering and the scoped B4 product activation are complete; the model-override behavior transition remains outside this activation.
- `student-interaction-v1` provides seven allowlisted, keyboard-operable renderers with server-bound answer schemas and an always-available text representation. Unknown or incomplete material fails closed to a sanitized text fallback and cannot authorize stage evidence.
- Student mobile classroom, supply station, voice player with timed highlighting, five explainable growth indicators, Parent live supervision/preferences, and Owner content/cost/learning-effect views.
- Planner and deterministic Mastery with evidence-derived scores, real activity-day streaks, idempotent Reward, STT/TTS adapters, versioned usage accounting, and deterministic review-queue consumption/rescheduling.
- Body-free, append-only learning-effect events calculate first-answer effective latency with coverage, interaction event share, completion by assistance, transfer accuracy, D+1/D+7 retention, post-EXPLAIN re-engagement, exit stage, and structured AI anomaly rate.
- Content provenance, deterministic validation, independent review, database release gate, quarantine, 142 released and source-linked knowledge points, and 15 curated demo questions.
- Full Student/Parent/Owner PostgreSQL + HTTP + role-projected realtime lifecycle E2E.
- Recoverable TTS sessions and Student-only post-session willingness reflections for a real seven-day trial.

The engineering acceptance matrix is [`docs/quality/v1-acceptance.md`](docs/quality/v1-acceptance.md). It deliberately leaves real seven-day child retention and real provider calls as external evidence, not simulated claims.

Run the external retention trial according to [`docs/quality/seven-day-child-trial.md`](docs/quality/seven-day-child-trial.md). Owner reporting is available at `/admin/trial`; it cannot manufacture or submit a child's reflection.

## Prerequisites

- Go 1.25 or newer
- Node.js 22 or newer
- npm 10 or newer

## Run Locally

```sh
docker compose up -d postgres
cp .env.example .env
set -a; source .env; set +a
go run ./server/cmd/migrate
go run ./server/cmd/api
```

In a second terminal:

```sh
npm install
npm run dev
```

Open `http://127.0.0.1:5173/`. The application expects authenticated HttpOnly `session_token` cookies. Tests create isolated users and sessions; production account provisioning remains an Owner-controlled deployment concern.

## Run With Docker

The production Compose stack builds the Vue/Nginx web image and Go API image,
runs PostgreSQL migrations as a one-shot gate, keeps PostgreSQL on an internal
network, and proxies both HTTP API and WebSocket traffic through one origin.

```sh
cp .env.production.example .env.production
# Edit secrets and deployment values, then:
docker compose --env-file .env.production -f compose.prod.yaml config --quiet
docker compose --env-file .env.production -f compose.prod.yaml up -d --build
```

The service binds to `127.0.0.1:18000` by default for an HTTPS reverse proxy.
The full deployment, first-Owner provisioning, price-catalog gate, backup,
update, and rollback procedure is documented in
[`docs/deployment/docker.md`](docs/deployment/docker.md).

For routine pre-merge releases to the test server, follow the maintainer-facing
Chinese guide [`docs/deployment/一键发布.md`](docs/deployment/%E4%B8%80%E9%94%AE%E5%8F%91%E5%B8%83.md).

Provision local or deployment accounts explicitly after migration. Passwords are read only from `PROVISION_PASSWORD` and are never accepted as command-line arguments:

```sh
PROVISION_PASSWORD='<at-least-12-bytes>' go run ./server/cmd/provision \
  -role STUDENT -email student@example.test -display-name 'Student' -grade 7

PROVISION_PASSWORD='<at-least-12-bytes>' go run ./server/cmd/provision \
  -role PARENT -email parent@example.test -display-name 'Parent' -student-id '<student UUID>'

PROVISION_PASSWORD='<at-least-12-bytes>' go run ./server/cmd/provision \
  -role OWNER -email owner@example.test -display-name 'Owner'
```

The command prints generated user/student identifiers, but never prints the password or a session token. Browser login creates a revocable server-side session and a `HttpOnly`, `SameSite=Strict` cookie.

Speech routes are enabled only when `OPENAI_API_KEY`, `OPENAI_STT_MODEL`, `OPENAI_TTS_MODEL`, and `OPENAI_TTS_VOICE` are all set. Every AI/STT/TTS provider must declare its provider/model billing identity. Add matching effective rows to `ai_price_catalog`; the server checks them before provider network calls, and prices are never hardcoded in business logic.

Tutor output requires both `OPENAI_TUTOR_MODEL` and the dedicated `OPENAI_TUTOR_OUTPUT_REVIEW_MODEL`. Their `provider:model` identities must differ; a missing or non-independent reviewer fails closed before student-visible output is persisted, published, or sent to TTS. Both models require effective `ai_price_catalog` rows, and the independent review call is metered under `TUTOR_OUTPUT_REVIEW` even when its structured result is later rejected. Reviewer HTTP 429 and 5xx responses use a bounded reviewer-only retry policy; exhaustion remains fail closed and returns a stable child-safe error without discarding typed Student input. `OPENAI_CONTENT_REVIEW_MODEL` remains a separate content-pipeline responsibility and must not be reused for Tutor output review.

Cost-accounting limitation for B6: the test gateway was observed to inject about 4,390 reviewer input tokens, including 3,840 cached tokens, and the observed review cost was about 4.8 times the Tutor generation cost for that sample. The catalog has no cache-write price field, so GPT-5.6+ cache-write cost can be understated; current cost totals must not be described as fully exact, and Terra's $2.50/1M cache-write reference price must not be stored in an audio price field.

Owner AI question generation is enabled with `OPENAI_CONTENT_GENERATION_MODEL`. The configured provider/model must have an effective `ai_price_catalog` row before any request is sent. Generation accepts only released, source-linked curriculum knowledge points and licensed content sources, creates server-owned `DRAFT` assets, and never skips deterministic validation, independent review, or Owner release. The Owner UI filters the catalog by subject, grade band, domain, unit metadata, name, and stable code. Configure a different provider/model for `OPENAI_CONTENT_REVIEW_MODEL`; generated content cannot be independently reviewed by the same provider/model that created it.

## Verify

```sh
go vet ./server/... ./schemas/...
go test -count=1 ./server/internal/... ./server/cmd/... ./schemas/...
export TEST_DATABASE_URL='postgres://learning_tutor:learning_tutor@127.0.0.1:55433/learning_tutor?sslmode=disable'
go test -count=1 ./server/tests/integration
npm test
npm run lint
npm run build
```

The integration command must target real PostgreSQL. A skipped integration test does not count as passing.

Integration gate baseline: top-level PASS >= 100, FAIL = 0, SKIP = 0, and TestIntegrationDatabaseConfiguredInCI must pass in CI.

The reproducible Chromium viewport suite is separate from the four core gates so `npm test` does not require a browser binary:

```sh
npx playwright install --with-deps chromium
npm run test:e2e
```

It uses Playwright at a 320 px layout viewport, switches the headless browser to a 360 px visible height, and exposes the matching reduced `visualViewport.height` while the keyboard is open. The authenticated Student classroom is exercised with intercepted API fixtures. Headless Chromium cannot summon an operating-system IME, so this is a deterministic keyboard-obscuration approximation for CI; a real-device run is still required before claiming coverage of OS-specific keyboard animation and IME behavior.

HTTP and WebSocket contracts are documented under [`docs/api/`](docs/api/).
