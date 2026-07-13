// Package schema is the single source of the JSON Schema for furrow's on-disk
// store. `furrow schema [task|meta|repo]` prints TaskV2 / MetaV2 / RepoV1;
// docs/schema/*.json are committed copies of the same bytes, and CI diffs them
// so they can never drift.
//
// The store is per-task shards (tasks/<id>.json, described by TaskV2), per-repo
// review shards (repos/<owner>__<repo>.json, described by RepoV1), plus one
// board-wide meta.json (described by MetaV2). Versioning: the version here
// numbers each schema document; the board LAYOUT version lives in meta.json's
// schema_version (currently 4 — 3 was the repos pivot, 2 pre-repos shards, 1 the
// monolithic index.json).
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
  "description": "Schema for one .furrow/tasks/<id>.json shard (a single task's metadata). The board-wide schema_version lives in .furrow/meta.json, never in a shard. v2 adds the required repos set (owner/repo identifiers; [] = draft, attached to no repo). Pin to a tagged URL or vendor this file.",
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
    "parent": { "type": "string" },
    "deps": { "type": "array", "items": { "type": "string" } },
    "refs": { "type": "array", "items": { "type": "string" } },
    "checklist": { "type": "array", "items": { "$ref": "#/$defs/checklistItem" } },
    "created": { "type": "string", "format": "date-time" },
    "updated": { "type": "string", "format": "date-time" },
    "closed": { "type": ["string", "null"], "format": "date-time" },
    "reviewed": { "type": ["string", "null"], "format": "date-time", "description": "when a human last reviewed this task (furrow review <id>); null = never. Tracked separately from updated." },
    "body": { "type": "string", "description": "relative path, e.g. bodies/t-0042.md" }
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
// layout version 4 (the review shards + per-task reviewed field); 3 was the
// repos pivot. v1 (which pinned layout 2) is retired, not dual-supported.
const MetaV2 = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "https://raw.githubusercontent.com/akira-toriyama/furrow/main/docs/schema/furrow.meta.v2.json",
  "title": "furrow meta v2",
  "description": "Schema for .furrow/meta.json — the one board-wide layout version. schema_version 4 = adds the per-repo review shards (repos/) and the per-task reviewed timestamp (3 = the repos pivot, 2 = pre-repos shards, 1 = the monolithic index.json). Pin to a tagged URL or vendor this file.",
  "type": "object",
  "additionalProperties": true,
  "$comment": "true, deliberately: furrow PRESERVES top-level keys it does not know (a field written by a newer furrow that did not bump the layout version) and re-emits them on write, so a meta.json furrow itself produces may legitimately carry extras. Declaring them invalid here would make this artifact call furrow's own output non-conforming. Typo detection therefore lives in furrow lint, which warns unknown-shard-key (blamed on the id \"meta\", since meta.json belongs to no task) — nothing else can, since nothing ever deletes an extra. This document has no nested objects, so the top-level-only limit of the passthrough has nothing to qualify here; the task shard's schema explains it.",
  "required": ["schema_version"],
  "properties": {
    "schema_version": { "const": 4 }
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
