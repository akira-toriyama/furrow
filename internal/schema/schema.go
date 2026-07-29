// Package schema is the single source of the JSON Schema for furrow's on-disk
// store. `furrow schema [task|meta|repo|epic]` prints TaskV2 / MetaV2 / RepoV1 /
// EpicV2;
// docs/schema/*.json are committed copies of the same bytes, and CI diffs them
// so they can never drift.
//
// The store is per-task shards (tasks/<id>.json, described by TaskV2), per-repo
// review shards (repos/<owner>__<repo>.json, described by RepoV1), per-epic
// shards (epics/<id>.json, described by EpicV2), plus one
// board-wide meta.json (described by MetaV2). Versioning: the version here
// numbers each schema document; the board LAYOUT version lives in meta.json's
// schema_version (currently 7 — 6 was the epic pivot, 5 the per-task type
// field, 4 the review shards + per-task reviewed, 3 the repos pivot,
// 2 pre-repos shards, 1 the monolithic index.json).
package schema

// TaskV2 is the JSON Schema (draft 2020-12) for one task shard: the object in a
// single .furrow/tasks/<id>.json file. Keep the required list and field set in
// lockstep with internal/core.Task's json tags. A shard carries NO
// schema_version — that lives in meta.json (see MetaV2). v2 adds the required
// "repos" set (repositories as a first-class concept); v1 is retired, not
// dual-supported — the published v1 document is deleted rather than silently
// rewritten.
const TaskV2 = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "https://raw.githubusercontent.com/akira-toriyama/furrow/main/docs/schema/furrow.task.v2.json",
  "title": "furrow task shard v2",
  "description": "Schema for one .furrow/tasks/<id>.json shard (a single task's metadata). The board-wide schema_version lives in .furrow/meta.json, never in a shard. v2 adds the required repos set (owner/repo identifiers; [] = draft, attached to no repo). Board layout v6 removed parent and type and added epic (the box a task belongs to). Pin to a tagged URL or vendor this file.",
  "type": "object",
  "additionalProperties": true,
  "$comment": "true, deliberately: furrow PRESERVES top-level keys it does not know (a field written by a newer furrow that did not bump the layout version) and re-emits them on write, so a shard furrow itself produces may legitimately carry extras. Declaring them invalid here would make this artifact call furrow's own output non-conforming. Typo detection therefore lives in furrow lint, which warns unknown-shard-key (naming the task) — nothing else can, since nothing ever deletes an extra. NOTE $defs/checklistItem below keeps additionalProperties:false, because passthrough is TOP-LEVEL ONLY — an unknown key inside a checklist item really is still dropped, and this schema must not promise what the marshaller does not do.",
  "required": ["id", "title", "status", "priority", "labels", "repos", "deps", "refs", "checklist", "created", "updated", "closed", "reviewed", "body"],
  "properties": {
    "id": { "type": "string", "description": "frozen id; == the shard filename stem and bodies/<id>.md stem" },
    "title": { "type": "string" },
    "status": { "type": "string", "description": "a lane from config.toml [lanes].order" },
    "priority": { "type": "integer", "description": "sparse 10-step ordering key" },
    "value": { "type": "integer", "minimum": 1, "maximum": 5, "description": "optional coarse 1..5 value (importance) estimate; absent = unset" },
    "effort": { "type": "integer", "minimum": 1, "maximum": 5, "description": "optional coarse 1..5 effort (cost) estimate; absent = unset" },
    "labels": { "type": "array", "items": { "type": "string" }, "description": "free-form tags (labels are NOT repos; see repos)" },
    "repos": { "type": "array", "items": { "type": "string", "pattern": "^[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?/[A-Za-z0-9._-]+$" }, "description": "repositories this task relates to, owner/repo form, sorted+deduped; [] = draft (no repo yet)" },
    "deps": { "type": "array", "items": { "type": "string" } },
    "refs": { "type": "array", "items": { "type": "string" } },
    "checklist": { "type": "array", "items": { "$ref": "#/$defs/checklistItem" } },
    "created": { "type": "string", "format": "date-time" },
    "updated": { "type": "string", "format": "date-time" },
    "closed": { "type": ["string", "null"], "format": "date-time" },
    "reviewed": { "type": ["string", "null"], "format": "date-time", "description": "when a human last reviewed this task (furrow review <id>); null = never. Tracked separately from updated." },
    "body": { "type": "string", "description": "relative path, e.g. bodies/t-0042.md" },
    "epic": { "type": "string", "description": "the epic (box) this task belongs to, 0..1 — an epics/<id>.json id. REQUIRED for an open task, enforced by furrow lint (epic-required) rather than at add time, so capture stays frictionless; absent is therefore legal on disk but flagged. furrow next scopes to the ACTIVE epic, which is why this is a schema field and not a label." }
  },
  "$defs": {
    "checklistItem": {
      "type": "object",
      "additionalProperties": false,
      "required": ["text", "done"],
      "properties": {
        "text": { "type": "string" },
        "done": { "type": "boolean" }
      }
    }
  }
}
`

// MetaV2 is the JSON Schema (draft 2020-12) for .furrow/meta.json: the one
// board-wide schema version, kept in its own file so a version bump touches one
// file and no shard becomes a merge point. Keep the const in lockstep with
// internal/core.Meta and core.SchemaVersion. The schema DOCUMENT stays v2 (its
// filename never changes) while the pinned layout version advances: it now pins
// layout version 7 (epic-to-epic deps; 6 was the epic pivot, 5 the per-task
// type field, 4 the review shards + per-task reviewed, 3 the repos pivot). v1
// (which pinned layout 2) is retired, not dual-supported.
const MetaV2 = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "https://raw.githubusercontent.com/akira-toriyama/furrow/main/docs/schema/furrow.meta.v2.json",
  "title": "furrow meta v2",
  "description": "Schema for .furrow/meta.json — the one board-wide layout version. schema_version 7 = epic-to-epic deps (the epics/<id>.json shard gains the required deps set); 6 = the epic pivot (per-task type/parent removed, per-task epic + the epics/ shard kind added); 5 = the per-task type field; 4 = the per-repo review shards (repos/) and the per-task reviewed timestamp (3 = the repos pivot, 2 = pre-repos shards, 1 = the monolithic index.json). Pin to a tagged URL or vendor this file.",
  "type": "object",
  "additionalProperties": true,
  "$comment": "true, deliberately: furrow PRESERVES top-level keys it does not know (a field written by a newer furrow that did not bump the layout version) and re-emits them on write, so a meta.json furrow itself produces may legitimately carry extras. Declaring them invalid here would make this artifact call furrow's own output non-conforming. Typo detection therefore lives in furrow lint, which warns unknown-shard-key (blamed on the id \"meta\", since meta.json belongs to no task) — nothing else can, since nothing ever deletes an extra. This document has no nested objects, so the top-level-only limit of the passthrough has nothing to qualify here; the task shard's schema explains it.",
  "required": ["schema_version"],
  "properties": {
    "schema_version": { "const": 7 }
  }
}
`

// RepoV1 is the JSON Schema (draft 2020-12) for one per-repo review shard: the
// object in a single .furrow/repos/<owner>__<repo>.json file. Like a task shard
// it is one entity per file and carries NO schema_version (meta.json owns the
// board-wide version). Keep it in lockstep with internal/core.RepoRecord's json
// tags. Both timestamps are nullable (null = never reviewed by that actor);
// last_reviewed is the human review clock the staleness nudge reads,
// last_agent_reviewed logs an agent sweep without advancing it.
// EpicV2 is the JSON Schema (draft 2020-12) for one epic shard: the object in a
// single .furrow/epics/<id>.json. Like the other shard kinds it carries no
// schema_version — that lives in meta.json (see MetaV2). v2 adds the required
// deps set (board layout v7): the epic ids this box waits on. v1 is retired,
// not dual-supported — the published v1 document is deleted rather than
// silently rewritten.
const EpicV2 = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "https://raw.githubusercontent.com/akira-toriyama/furrow/main/docs/schema/furrow.epic.v2.json",
  "title": "furrow epic shard v2",
  "description": "Schema for one .furrow/epics/<id>.json shard (a single epic: a box of work with member tasks). The board-wide schema_version lives in .furrow/meta.json, never in a shard. v2 adds the required deps set (board layout v7): the epic ids this box waits on — information, not enforcement (epic activate warns about an open dep and proceeds). Pin to a tagged URL or vendor this file.",
  "type": "object",
  "additionalProperties": true,
  "$comment": "true, deliberately: furrow PRESERVES top-level keys it does not know (a field written by a newer furrow that did not bump the layout version) and re-emits them on write, so a shard furrow itself produces may legitimately carry extras. Declaring them invalid here would make this artifact call furrow's own output non-conforming. Typo detection therefore lives in furrow lint, which warns unknown-shard-key (naming the epic) — nothing else can, since nothing ever deletes an extra.",
  "required": ["id", "title", "goal", "active", "labels", "repos", "meta", "created", "updated", "closed", "body", "deps"],
  "properties": {
    "id": { "type": "string", "description": "frozen id, e- prefixed; == the shard filename stem and bodies/<id>.md stem" },
    "title": { "type": "string" },
    "goal": { "type": "string", "description": "the closing condition in one line. OPTIONAL in practice (\"\" is valid and never linted): a box whose title already says it gains nothing from restating it. Its value is that epic ls can show it." },
    "active": { "type": "boolean", "description": "this is the box currently being worked. At most one active epic per repo, and an epic naming several repos consumes a slot in each. furrow next scopes to it." },
    "labels": { "type": "array", "items": { "type": "string" }, "description": "free-form tags, same semantics as a task's" },
    "repos": { "type": "array", "items": { "type": "string", "pattern": "^[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?/[A-Za-z0-9._-]+$" }, "description": "repositories this box spans, owner/repo form, sorted+deduped. A repo-less epic cannot be activated (it would bypass the one-active-per-repo rule)." },
    "meta": { "type": "object", "additionalProperties": { "type": "string" }, "description": "free-form key/value furrow stores, round-trips and NEVER interprets. Flat (string values only) so the deterministic byte recipe holds: encoding/json sorts a map's keys, and a string cannot reintroduce float-precision churn. Not queryable by design — read it with jq." },
    "created": { "type": "string", "format": "date-time" },
    "updated": { "type": "string", "format": "date-time" },
    "closed": { "type": ["string", "null"], "format": "date-time", "description": "null while open; an epic has no lane — open/closed is its whole state model" },
    "body": { "type": "string", "description": "relative path, e.g. bodies/e-k3m9.md — the SAME directory task bodies live in (ids are prefix-disjoint)" },
    "deps": { "type": "array", "items": { "type": "string" }, "description": "epic ids this box waits on (open after those close), sorted+deduped, acyclic. Information, not enforcement: epic activate warns about a still-open dep and proceeds, a dep on a closed epic is satisfied, and revisit raises epic_dep_done when every dep is closed. furrow lint backstops the graph (epic-dep-cycle, epic-dep-missing, epic-dep-open)." }
  }
}
`

const RepoV1 = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "https://raw.githubusercontent.com/akira-toriyama/furrow/main/docs/schema/furrow.repo.v1.json",
  "title": "furrow repo review shard v1",
  "description": "Schema for one .furrow/repos/<owner>__<repo>.json shard (a single repo's review record). The board-wide schema_version lives in .furrow/meta.json, never in a shard. Pin to a tagged URL or vendor this file.",
  "type": "object",
  "additionalProperties": true,
  "$comment": "true, deliberately: furrow PRESERVES top-level keys it does not know (a field written by a newer furrow that did not bump the layout version) and re-emits them on write, so a shard furrow itself produces may legitimately carry extras. Declaring them invalid here would make this artifact call furrow's own output non-conforming. Typo detection therefore lives in furrow lint, which warns unknown-shard-key (naming the owner/repo) — nothing else can, since nothing ever deletes an extra. This document has no nested objects, so the top-level-only limit of the passthrough has nothing to qualify here; the task shard's schema explains it.",
  "required": ["repo", "last_reviewed", "last_agent_reviewed"],
  "properties": {
    "repo": { "type": "string", "pattern": "^[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?/[A-Za-z0-9._-]+$", "description": "the owner/repo this record reviews" },
    "last_reviewed": { "type": ["string", "null"], "format": "date-time", "description": "when a human last reviewed this repo's backlog (the staleness-nudge clock); null = never" },
    "last_agent_reviewed": { "type": ["string", "null"], "format": "date-time", "description": "when an agent last swept this repo; recorded but does not advance the human nudge clock; null = never" }
  }
}
`
