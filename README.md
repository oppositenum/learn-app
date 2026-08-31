# AI Learning Tutor

AI Learning Tutor is a mobile-first, five-subject Socratic learning prototype for primary and junior-secondary students. The frozen product source is [`docs/product/互动式学习_V1.md`](docs/product/%E4%BA%92%E5%8A%A8%E5%BC%8F%E5%AD%A6%E4%B9%A0_V1.md); non-negotiable engineering rules are in [`AGENTS.md`](AGENTS.md).

## Implemented V1 Surface

- Student, Parent, and Owner authorization with explicit Parent-Student binding, plus answer-free Parent ability and activity reports.
- PostgreSQL-level public/private answer separation and authenticated Student non-disclosure tests.
- Five-subject curriculum with all 132 assessable primary/junior-secondary skeleton entries from the V1 product specification, 10 preserved high-quality detail points, prerequisite graph, cross-subject graph, abilities, and misconceptions. The 142 released points are organized into 44 useful subject/grade-band domains rather than the original broad three-domain demo taxonomy.
- Complete Tutor transition graph, three-round Socratic fuse, persisted emotion deescalation, functional hint/parallel-example support, automatic released-prerequisite backtrack/return, and explicit voice explanation return.
- Student mobile classroom, supply station, voice player with timed highlighting, growth view, Parent live supervision/preferences, and Owner content/cost views.
- Planner, deterministic Mastery/Review with evidence-derived scores, real activity-day streaks, idempotent Reward, STT/TTS adapters, and versioned usage accounting.
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

Tutor and independent content review are enabled separately with `OPENAI_TUTOR_MODEL` and `OPENAI_CONTENT_REVIEW_MODEL`. `CONTENT_GENERATOR_IDENTITY` must not match the independent reviewer identity. The API records a priced usage row even when structured provider output is later rejected by schema or teaching-safety validation.

Owner AI question generation is enabled with `OPENAI_CONTENT_GENERATION_MODEL`. The configured provider/model must have an effective `ai_price_catalog` row before any request is sent. Generation accepts only released, source-linked curriculum knowledge points and licensed content sources, creates server-owned `DRAFT` assets, and never skips deterministic validation, independent review, or Owner release. The Owner UI filters the catalog by subject, grade band, domain, unit metadata, name, and stable code. Configure a different provider/model for `OPENAI_CONTENT_REVIEW_MODEL`; generated content cannot be independently reviewed by the same provider/model that created it.

## Verify

```sh
go test ./...
go vet ./server/...
npm test
npm run build
npm run lint
```

Run the real PostgreSQL gates:

```sh
TEST_DATABASE_URL='postgres://learning_tutor:learning_tutor@127.0.0.1:55433/learning_tutor?sslmode=disable' \
  go test ./server/tests/integration -count=1 -v
```

HTTP and WebSocket contracts are documented under [`docs/api/`](docs/api/).
