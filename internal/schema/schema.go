// Package schema is the single source of the JSON Schema for furrow's on-disk
// store. `furrow schema [task|meta]` prints TaskV2 / MetaV2; docs/schema/*.json
// are committed copies of the same bytes, and CI diffs them so they can never
// drift.
//
// The store is per-task shards (tasks/<id>.json, described by TaskV2) plus one
// board-wide meta.json (described by MetaV2). Versioning: the version here
// numbers each schema document; the board LAYOUT version lives in meta.json's
// schema_version (currently 3 — 2 was pre-repos shards, 1 the monolithic
// index.json).
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
  "additionalProperties": false,
  "required": ["id", "title", "status", "priority", "labels", "repos", "deps", "refs", "checklist", "created", "updated", "closed", "body"],
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
// internal/core.Meta and core.SchemaVersion. v2 pins layout version 3 (the
// repos pivot flag-day); v1 (which pinned layout 2) is retired, not
// dual-supported — the published v1 document is deleted rather than silently
// rewritten.
const MetaV2 = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "https://raw.githubusercontent.com/akira-toriyama/furrow/main/docs/schema/furrow.meta.v2.json",
  "title": "furrow meta v2",
  "description": "Schema for .furrow/meta.json — the one board-wide layout version. schema_version 3 = shards whose tasks carry the required first-class repos set (2 = pre-repos shards, 1 = the monolithic index.json). Pin to a tagged URL or vendor this file.",
  "type": "object",
  "additionalProperties": false,
  "required": ["schema_version"],
  "properties": {
    "schema_version": { "const": 3 }
  }
}
`
