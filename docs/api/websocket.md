# WebSocket Event Contract

Browser WebSockets authenticate through the same-origin HttpOnly session cookie.

- Student: `/ws/student/{student_id}`; only the matching Student may subscribe.
- Parent: `/ws/parent/{student_id}`; only an actively bound Parent may subscribe.
- Slow observers are dropped per event buffer policy and never block the classroom transaction.

The Parent client subscribes as soon as a child is selected, even when that child has no active session. `SESSION_STARTED` switches the live projection to the new session, answer events update the visible supervision state immediately, and the client refreshes the authoritative Parent REST projection after each event burst. Clients reconnect with bounded exponential backoff after an unexpected close.

The server projects separate representations before serialization:

```text
Tutor event
  -> StudentEventDTO (safe teaching action/message)
  -> ParentEventDTO  (answer, analysis, misconception, action reason)
```

Common envelope fields are `event_id`, `session_id`, `sequence`, `type`, `created_at`, and `payload`. Parent envelopes additionally contain `student_id`.

Runtime-persisted and broadcast event types are:

| Lifecycle | Event types |
|---|---|
| Session | `SESSION_STARTED`, `QUESTION_PRESENTED`, `SESSION_PAUSED`, `SESSION_RESUMED`, `SESSION_ABANDONED`, `SESSION_COMPLETED` |
| Answer | `ANSWER_SUBMITTED`, `ANSWER_ANALYZED` |
| Tutor | `TUTOR_ACTION_SELECTED`, `HINT_REQUESTED`, `AI_TURN_COMPLETED` |
| Remediation | `BACKTRACK_STARTED`, `BACKTRACK_COMPLETED`, `VOICE_EXPLAIN_STARTED`, `VOICE_EXPLAIN_COMPLETED` |
| Adaptive engines | `MASTERY_UPDATED`, `REWARD_GRANTED`, `PLAN_MODIFIED` |
| Parent | `PARENT_INTERVENTION` |

`SESSION_PAUSED`, `SESSION_RESUMED`, and `SESSION_ABANDONED` are persisted and broadcast with `status` and checkpointed `active_seconds` in both role-specific payloads. They can be produced by an explicit Student lifecycle request or stale-session recovery. Clients still reconcile against the authoritative REST session after event bursts.

`AI_TURN_STARTED` and `AI_TURN_STREAM` remain reserved protocol types. The current OpenAI Responses adapter is request/response based and therefore does not emit fake streaming events. A future streaming adapter must emit these events from real provider lifecycle signals.

Student payloads must not contain `correct_answer`, solutions, scoring keys, private misconception mappings, provider raw logs, or the submitted answer echoed back by the server. `ANSWER_SUBMITTED` only acknowledges receipt on the Student channel; the authenticated Parent projection contains the answer.
