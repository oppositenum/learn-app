# Docker production deployment

This deployment packages the V1 modular monolith as four containers:

```text
HTTPS reverse proxy
       |
       v
web (Nginx + Vue, :8080)
       |
       +-- /api/* and /ws/* --> api (Go, :8080) --> model provider
                                  |
                                  v
                            postgres (:5432)

migrate runs to completion before api starts.
```

PostgreSQL is connected only to Docker's internal `backend` network and has no
published host port. The Web service publishes `127.0.0.1:18000` by default so
the public TLS reverse proxy can remain the only Internet-facing process.

## 1. Prepare the host

Required host software:

- Docker Engine 27 or newer with Compose v2.
- An HTTPS reverse proxy and a real hostname for non-local use.
- Persistent disk sized for PostgreSQL data and backups.

Create the deployment environment without editing the checked-in example:

```sh
cp .env.production.example .env.production
chmod 600 .env.production
```

Set a long random `POSTGRES_PASSWORD`, then set the same URL-encoded password
inside `DATABASE_URL`. Keep `WEB_BIND_ADDR=127.0.0.1` when a host reverse proxy
is used. Provider variables may stay empty; the core application remains
available, while AI/STT/TTS features stay disabled.

Do not put provider keys, account passwords, raw child audio, or database dumps
in the repository.

## 2. Validate and start

Always render the effective Compose configuration before changing runtime
state. The first command also detects missing required variables.

```sh
docker compose --env-file .env.production -f compose.prod.yaml config --quiet
docker compose --env-file .env.production -f compose.prod.yaml build
docker compose --env-file .env.production -f compose.prod.yaml up -d
```

The startup dependency order is:

```text
postgres healthy -> migrate exits 0 -> api healthy -> web starts
```

Inspect actual state and migration output:

```sh
docker compose --env-file .env.production -f compose.prod.yaml ps
docker compose --env-file .env.production -f compose.prod.yaml logs migrate
docker compose --env-file .env.production -f compose.prod.yaml logs --tail=100 api web
curl --fail http://127.0.0.1:18000/app-health
curl --fail http://127.0.0.1:18000/healthz
```

For a temporary local HTTP check, set `SESSION_COOKIE_SECURE=false`. Real
deployments should keep it `true` and expose the service only through HTTPS.

## 3. Terminate HTTPS

Point the host reverse proxy at `http://127.0.0.1:18000`. It must support long
WebSocket connections and preserve the original host. A minimal Nginx location
is:

```nginx
map $http_upgrade $connection_upgrade {
    default upgrade;
    '' close;
}

location / {
    proxy_pass http://127.0.0.1:18000;
    proxy_http_version 1.1;
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto https;
    proxy_set_header Upgrade $http_upgrade;
    proxy_set_header Connection $connection_upgrade;
    proxy_read_timeout 3600s;
}
```

The public reverse proxy owns certificates, HTTP-to-HTTPS redirection, HSTS,
request-rate limits, and public access logs. Do not publish the API or database
ports separately.

## 4. Provision the first Owner

Migrations do not create login accounts. After the stack is healthy, provision
an Owner explicitly. The password is passed only as an environment variable and
is never printed by the command.

```sh
docker compose --env-file .env.production -f compose.prod.yaml run --rm \
  -e PROVISION_PASSWORD='<at-least-12-bytes>' \
  api /app/provision -role OWNER -email owner@example.com -display-name Owner
```

Create Student and Parent accounts from the authenticated Owner UI. This keeps
identity creation and Parent-Student links under Owner control.

## 5. Enable AI features

Three capabilities call providers and are configured separately in
`.env.production`. Tutor text and content-pipeline text each use two independent
channels; speech still uses the OpenAI variables.

| Capability | Variables |
| --- | --- |
| Tutor text | `TUTOR_GENERATOR_*` and `TUTOR_REVIEWER_*` |
| Content-pipeline text | `CONTENT_GENERATOR_*` and `CONTENT_REVIEWER_*` |
| STT/TTS | `OPENAI_API_KEY`, `OPENAI_STT_MODEL`, `OPENAI_TTS_MODEL`, `OPENAI_TTS_VOICE` |

Each text channel takes `_PROVIDER`, `_BASE_URL`, `_API_KEY`, `_MODEL`, and
optionally `_API_SHAPE` (`responses` or `chat_completions`) and
`_REQUEST_OVERLAY`. `_BASE_URL` is required for any provider other than
`openai`. `_API_SHAPE` defaults to `chat_completions` for Tutor and the content
pipeline. Tutor requires `_REQUEST_OVERLAY` set to the actual provider's
syntax so reasoning mode stays off. Compose does not fill Doubao or Qwen
overlay JSON; an empty overlay fails startup when Tutor is enabled.

Owner AI question generation additionally requires:

- A `CONTENT_REVIEWER_*` identity that differs from `CONTENT_GENERATOR_*`.
  Equal `provider:model` identities fail at startup, not at first use.
- A currently effective `ai_price_catalog` row for each configured identity,
  matching its provider and exact model.

> **Retired variables block startup.** `OPENAI_CONTENT_GENERATION_MODEL` and
> `OPENAI_CONTENT_REVIEW_MODEL` no longer configure anything, and the API
> container **exits on start** while either is present. Remove both from
> `.env.production` before deploying, or the release will fail at container
> start rather than degrade silently. `OPENAI_TUTOR_MODEL` and
> `OPENAI_TUTOR_OUTPUT_REVIEW_MODEL` are different: they still work as an
> OpenAI-only fallback, but prefer the `TUTOR_*` channels.

Also keep every `*_CONTEXT_CACHE` variable unset or false; enabling one is
refused at startup because `ai_price_catalog` has no cache-write price field.

`compose.prod.yaml` and `compose.deploy.yaml` declare an explicit
`environment:` map with no `env_file:`, so a variable reaches the API container
only if it is listed there. Both files forward every variable above. Because
that coupling is easy to break silently — a channel variable added to the Go
config but not to compose produces a deployment where the channel cannot be
configured at all — it is checked:

```sh
bash scripts/assert-provider-env-forwarding.sh
```

The script derives the expected variable list from the Go configuration rather
than repeating it, so a channel added later is caught without editing the
script. Run it after changing provider configuration, compose, or
`.env.production.example`.

Tutor, STT, and TTS have the same price-catalog gate. Do not invent or hard-code
prices: insert the supplier's effective, versioned prices through an
Owner-controlled database change. Every request is rejected before provider
network access when its effective catalog entry is absent.

Generated questions always enter `DRAFT`. They still require deterministic
validation, independent review, and explicit Owner release before Student
runtime can read them.

## 6. Backup, update, and rollback

Create and verify a logical backup before every application or schema update:

```sh
mkdir -p backups
docker compose --env-file .env.production -f compose.prod.yaml exec -T postgres \
  sh -c 'pg_dump -U "$POSTGRES_USER" -d "$POSTGRES_DB" -Fc' \
  > "backups/learning_tutor-$(date +%Y%m%d-%H%M%S).dump"
pg_restore --list backups/learning_tutor-*.dump >/dev/null
```

Store backups outside the repository and test restoration on an isolated
database.

To update, build the intended source revision, run the migration job, and only
then replace services:

```sh
docker compose --env-file .env.production -f compose.prod.yaml build
docker compose --env-file .env.production -f compose.prod.yaml up -d
docker compose --env-file .env.production -f compose.prod.yaml ps
```

Application images can be rolled back to a previously tagged image, but SQL
migrations in this repository are forward-only. Do not assume that replacing
an image reverses a schema migration. Restore a verified pre-update backup into
an isolated PostgreSQL instance, validate it, and switch over deliberately when
a database rollback is required.

## 7. Operational checks

Monitor at minimum:

- container health and restart count;
- PostgreSQL volume usage, connections, and backup age;
- HTTP 5xx, WebSocket disconnects, and login lockouts;
- provider latency/errors and `ai_usage_records` coverage;
- missing/effective `ai_price_catalog` rows;
- content validation, review, release, and quarantine audit state.

`/healthz` is a liveness endpoint. A complete release check must also log in,
load a role-appropriate page, open a WebSocket, and verify that a Student API
cannot serialize private answer fields.

## 8. One-click test releases

Repository maintainers can publish an unmerged, already reviewed commit to the
test server without installing Docker locally. The two manual GitHub Actions
workflows are deliberately separate:

The maintainer-facing Chinese instructions are in
[`一键发布.md`](%E4%B8%80%E9%94%AE%E5%8F%91%E5%B8%83.md).

- **Test Deploy** reruns all Go, PostgreSQL, and Web gates for an exact 40-character
  commit SHA, builds both `linux/amd64` images, backs up PostgreSQL, and starts
  the test release.
- **Test Rollback** restores the image tag that was active before Test Deploy.
  It does not reverse forward-only database migrations.

The test server receives images through a restricted SSH gateway. GitHub never
receives the private registry password: the gateway loads the two verified
images and uses the server's existing registry login to publish them. The key
cannot open an interactive shell and accepts only `upload`, `deploy`,
`rollback`, and `status` operations.

### One-time administrator setup

Create a dedicated Ed25519 key for the `test` GitHub Environment. Keep the
private key out of the repository and install only its public half on the test
server:

```sh
sudo ./deploy/install-test-release-gateway.sh /secure/path/test-deploy.pub
```

Configure these values in the GitHub Environment named `test`:

```text
Secret   TEST_DEPLOY_SSH_KEY      dedicated private key
Secret   TEST_DEPLOY_KNOWN_HOSTS  pinned SSH known_hosts entry
Variable TEST_DEPLOY_HOST         test-server hostname
Variable TEST_DEPLOY_USER         learnapp-deploy
```

Do not reuse a personal or root SSH private key. Do not copy the server's
Docker configuration into GitHub. The server-side registry login remains on
the server.

### Publish a version for functional testing

Open **Actions -> Test Deploy -> Run workflow**, then enter:

```text
commit_sha   the reviewed full 40-character SHA
release_name a short lowercase label such as b2-review-lifecycle
confirmation DEPLOY
```

The workflow locks the exact revision before running tests. It generates one
tag for both images, such as
`test-b2-review-lifecycle-20260906-0a789bb`, and refuses deployment unless the
target revision's own integration threshold passes with no failures or skips.

After Actions reports success, use the browser to log in as each affected role,
exercise the changed workflow, and confirm that its WebSocket connects. Health
checks alone are not functional acceptance.

### Restore the previous version

After browser testing, open **Actions -> Test Rollback -> Run workflow**, enter
`ROLLBACK`, and run it. The workflow verifies the restored API, Web, PostgreSQL,
application health endpoint, and API health endpoint. Do not leave a test tag
running after the test is complete.

Every Test Deploy creates and validates a PostgreSQL custom-format backup before
changing `APP_IMAGE_TAG`. A failed deployment automatically attempts to restore
the previous application images. Because migrations are forward-only, a schema
problem still requires an administrator to validate and restore the recorded
backup deliberately; the workflow never drops or automatically restores the
database.
