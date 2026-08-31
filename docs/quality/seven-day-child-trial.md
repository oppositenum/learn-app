# Seven-Day Child Trial Protocol

This protocol supplies the external evidence required by V1 success condition 43.1. Automated fixtures prove the recording and reporting mechanism only; they do not prove that a real child willingly used the product.

## Entry Gate

- A guardian has explicitly consented to the trial outside the child-facing classroom.
- The child understands that stopping or pausing has no penalty.
- The trial uses a dedicated real Student account. Test and seeded accounts are excluded.
- No operator, Parent, or Owner submits the child's reflection.

## Daily Evidence

For each calendar day in the trial:

1. The child independently opens the product and completes at least one classroom session.
2. The server records the completed session in `student_activity_days`.
3. After `COMPLETE`, the child chooses one reflection: `CONTINUE_TOMORROW`, `PAUSE`, or `STOP`.
4. The authenticated Student endpoint binds that reflection to the completed session and its activity date.

The product must continue to work if the child chooses `PAUSE` or `STOP`. Rewards, access, and Parent controls must not pressure the child to select `CONTINUE_TOMORROW`.

## Pass Rule

Condition 43.1 passes only when the Owner trial report shows one historical window with:

```text
7 consecutive activity dates
+ at least 1 completed session on each date
+ CONTINUE_TOMORROW submitted by the Student on each date
```

The report uses the longest historical willing streak, so a later pause does not erase a legitimately completed seven-day window. The current willing streak is reported separately.

## Invalid Evidence

- Integration tests, fixture clocks, manually inserted activity rows, or browser automation.
- A Parent, Owner, developer, or researcher choosing on the child's behalf.
- Seven activity rows without seven Student reflections.
- Reflections attached to active, abandoned, or another child's session.
- Screenshots without matching server records.

## Review

The Owner reviews `/admin/trial` and exports the observation notes separately. Product readiness still requires checking qualitative concerns, withdrawal reasons, provider quality, and any safety incident; the boolean report is the minimum V1 core criterion, not a substitute for safeguarding review.
