// Package schema is the single source of the JSON Schema for .furrow/index.json.
// `furrow schema` prints IndexV1; docs/schema/furrow.index.v1.json is a committed
// copy of the same bytes, and CI diffs the two so they can never drift.
//
// Versioning: v2 = a breaking layout change; v1.x stays additive-only.
package schema

// IndexV1 is the JSON Schema (draft 2020-12) for schema_version 1. Keep the
// required list and field set in lockstep with internal/core.Task's json tags.
const IndexV1 = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "https://raw.githubusercontent.com/akira-toriyama/furrow/main/docs/schema/furrow.index.v1.json",
  "title": "furrow index v1",
  "description": "Schema for .furrow/index.json. Pin to a tagged URL or vendor this file. v2 = breaking; v1.x = additive-only.",
  "type": "object",
  "additionalProperties": false,
  "required": ["schema_version", "tasks"],
  "properties": {
    "schema_version": { "const": 1 },
    "tasks": { "type": "array", "items": { "$ref": "#/$defs/task" } }
  },
  "$defs": {
    "task": {
      "type": "object",
      "additionalProperties": false,
      "required": ["id", "title", "status", "priority", "labels", "deps", "refs", "checklist", "created", "updated", "closed", "body"],
      "properties": {
        "id": { "type": "string", "description": "frozen id; bodies/<id>.md stem" },
        "title": { "type": "string" },
        "status": { "type": "string", "description": "a lane from config.toml [lanes].order" },
        "priority": { "type": "integer", "description": "sparse 10-step ordering key" },
        "value": { "type": "integer", "minimum": 1, "maximum": 5, "description": "optional coarse 1..5 value (importance) estimate; absent = unset" },
        "effort": { "type": "integer", "minimum": 1, "maximum": 5, "description": "optional coarse 1..5 effort (cost) estimate; absent = unset" },
        "labels": { "type": "array", "items": { "type": "string" } },
        "parent": { "type": "string" },
        "deps": { "type": "array", "items": { "type": "string" } },
        "refs": { "type": "array", "items": { "type": "string" } },
        "checklist": { "type": "array", "items": { "$ref": "#/$defs/checklistItem" } },
        "created": { "type": "string", "format": "date-time" },
        "updated": { "type": "string", "format": "date-time" },
        "closed": { "type": ["string", "null"], "format": "date-time" },
        "body": { "type": "string", "description": "relative path, e.g. bodies/t-0042.md" }
      }
    },
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
