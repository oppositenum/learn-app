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

Question generation requires all of the following:

- `OPENAI_API_KEY` and `OPENAI_CONTENT_GENERATION_MODEL` in `.env.production`.
- A currently effective `ai_price_catalog` row matching provider `openai` and
  the exact configured model.
- A different configured reviewer identity/model before independent AI review.

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
