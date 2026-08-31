# Student Answer Non-Disclosure Boundary

Task 004 is the first V1 product safety gate. It is enforced as a server ownership boundary, not as frontend hiding.

## Data Ownership

- `questions` contains only classroom-safe public fields.
- `question_private_answers` contains correct answers, full solutions, scoring keys, private misconception mappings, and private hint policy.
- Runtime queries require `questions.status = 'RELEASED'`.

## Capability Split

- `content.PublicQuestionReader` can return only `QuestionPublic`.
- Student handlers depend only on `PublicQuestionReader`.
- `content.TeachingQuestionReader` returns `QuestionForTeaching`, which combines public and private data for future server-side Tutor use.
- Parent and Owner read models must be implemented separately and must never reuse a complete event or response payload with frontend-only field hiding.

## Automated Gate

`server/tests/integration/security_gate_test.go` creates an isolated PostgreSQL schema, applies the real migrations, inserts both a released public question and its private answer, and then calls the authenticated Student HTTP route. The test fails if:

- any forbidden private key exists at any response depth;
- the private answer canary occurs in the response body;
- a Parent session can call the Student endpoint;
- a `DRAFT` question is returned;
- the five V1 subjects are not seeded; or
- an unrelated parent can supervise the student.

Run the real PostgreSQL gate with:

```sh
docker compose up -d postgres
TEST_DATABASE_URL='postgres://learning_tutor:learning_tutor@127.0.0.1:55433/learning_tutor?sslmode=disable' \
  go test ./server/tests/integration -run TestPostgresStudentAnswerNonDisclosureGate -v
```
