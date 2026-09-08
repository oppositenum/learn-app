# AI Learning Tutor Engineering Rules

## Source Of Truth

Read this file and `docs/product/互动式学习_V1.md` before changing the repository. The document's V1.1 revision is the sole current product and acceptance baseline and supersedes the original V1 frozen baseline preserved at commit `a009be5d43c8edcfb6190a2c8a3967b04cf2758f`. Implement the numbered tasks in order and do not silently broaden a task.

Task 004, Student Answer Non-Disclosure, is the first product safety gate. Do not implement a real Tutor or AI classroom until automated tests prove that Student APIs cannot serialize private answer data.

## Non-Negotiable Constraints

1. Student APIs must never return private answer fields.
2. The Teaching Agent must not have repository or server administration permissions.
3. Mastery is decided by the rules engine; an LLM must not write mastery state directly.
4. Runtime classrooms must not read content in `DRAFT` state.
5. Only `RELEASED` content may enter a runtime classroom.
6. Every AI, STT, and TTS request must record usage and the applicable versioned price catalog entry.
7. Parent and Student realtime DTOs must be separate server-side representations.
8. A `SendAnswerToStudent` API must not exist for Parent or any other role.
9. The default Socratic failed-round limit is three effective attempts.
10. Cross-subject backtracking must preserve and return to the original task context.
11. Every animation must support `prefers-reduced-motion`.
12. Student and Parent experiences must remain usable at a 320 px viewport width.
13. Do not copy third-party commercial question banks without clear licensing.
14. Core teaching state machines require unit and integration tests.

## Architecture Boundaries

- Keep V1 as a modular monolith: Vue Web, Go API, PostgreSQL, and WebSocket.
- Support `MATH`, `CHINESE`, `ENGLISH`, `PHYSICS`, and `CHEMISTRY` in shared models. Do not hard-code Math-only flows.
- Keep public question data and private answer data physically or logically isolated.
- The Student browser calls the Go API only. It must never call a teaching model provider directly.
- Teaching model providers are accessible only through the server-side Teaching Agent Gateway and an explicit restricted tool set.
- Validate all structured AI output against schemas before business logic consumes it.
- Keep provider prices in a versioned catalog. Do not hard-code model prices in business logic.
- Preserve content provenance, validation, review, release, quarantine, and audit history.

## Development Workflow

- Keep changes within the active numbered task.
- Add tests in proportion to the affected security or state boundary.
- Run `go vet ./server/... ./schemas/...`, `go test -count=1 ./server/internal/... ./server/cmd/... ./schemas/...`, `go test -count=1 ./server/tests/integration`, `npm test`, `npm run lint`, and `npm run build` before declaring a task complete.
- Run the integration command with `TEST_DATABASE_URL` pointing to real PostgreSQL. A skipped integration test does not count as passing.
- Integration gate baseline: top-level PASS >= 74, FAIL = 0, SKIP = 0, and TestIntegrationDatabaseConfiguredInCI must pass in CI.
- Integration tests must derive business dates from the same PostgreSQL session as their fixtures; do not mix Go process dates with PostgreSQL session dates.
- Before using a timezone run as defect-fix evidence, execute `TEST_DATABASE_URL='<real postgres>' scripts/assert-timezone-date-split.sh "$TZ_SPLIT_ZONE"`. The assertion must pass before the test run; an unchanged process/database calendar date is invalid evidence.
- Never commit secrets, provider keys, raw child audio, or unnecessary personal data.
- Use migrations for persistent schema changes and keep PostgreSQL as the production database contract.
