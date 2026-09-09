# `student-interaction-v1`

Student session responses contain a server-normalized `interaction` object. A valid structured material has all of:

- `version: "student-interaction-v1"`
- one allowlisted `renderer`
- a renderer-specific public `scene`
- a JSON `answer_schema` exactly bound to the public item identifiers
- a non-empty `accessible_fallback` text representation
- `fallback: false`

The renderer allowlist is `SINGLE_CHOICE`, `MULTIPLE_CHOICE`, `ORDERING`, `MATCHING`, `GROUPING`, `NUMBER_LINE`, and `FILL_BLANKS`. The UI uses native radio buttons, checkboxes, buttons, selects, range/number inputs, and text inputs, so every operation is available by keyboard and to accessibility APIs without drag-only gestures.

Unknown versions, unknown renderers, malformed scenes, missing text representations, and schemas that are not exactly bound to the scene resolve to:

```json
{
  "version": "student-text-fallback-v1",
  "renderer": "TEXT_FALLBACK",
  "accessible_fallback": "<the public task prompt>",
  "fallback": true
}
```

The server removes the unrecognized scene from the Student payload. A complete `READY` four-stage lineage cannot start with fallback material. If released material drifts during an open stage session, the Student receives the text representation and a recovery state, but no response can authorize stage evidence until valid material is restored.

## Structured responses

| Renderer | Response |
|---|---|
| `SINGLE_CHOICE` | `{"selected_option_ids":["..."]}` with exactly one public option ID |
| `MULTIPLE_CHOICE` | `{"selected_option_ids":["...","..."]}` |
| `ORDERING` | `{"ordered_item_ids":["...","..."]}` containing every public item ID once |
| `MATCHING` | `{"pairs":[{"left_id":"...","right_id":"..."}]}` with every left ID once |
| `GROUPING` | `{"placements":[{"item_id":"...","group_id":"..."}]}` with every item ID once |
| `NUMBER_LINE` | `{"value":0}` within the published minimum, maximum, and step |
| `FILL_BLANKS` | `{"values":[{"slot_id":"...","value":"..."}]}` with every slot ID once |

The response body for a stage answer is:

```json
{
  "operation_id": "<uuid>",
  "stage": "ORIGINAL",
  "task_id": "<uuid>",
  "task_version": "<content version>",
  "response": {"selected_option_ids":["..."]}
}
```

The client retains the same response and `operation_id` across transient retries. The operation digest binds the canonical structured response to the session, stage, task, task version, attempt kind, and support type. Student responses are not persisted in the stage attempt or evidence tables and are never included in Student realtime payloads.
