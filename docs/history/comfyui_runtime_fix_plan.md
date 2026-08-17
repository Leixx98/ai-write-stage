# ComfyUI Canvas Runtime Fix Plan

This document defines the smallest compatible fix for the two observed runtime
problems: an exposed field cannot be unchecked, and a prompt value does not
reach the ComfyUI node while an image output is returned as a non-previewable
file. It is an implementation contract for the backend and frontend agents;
it does not change the public route names.

## 1. Field editor and unexpose contract

`CanvasDocument.Fields` is the single canonical list of exposed fields. A field
is exposed exactly when one entry exists in this list. The checkbox is not a
secondary `exposed` flag: an unchecked row must remove the entry from the list.
The corresponding `CanvasNode.ExposedFieldIDs`, workflow config `fields`,
`bindings`, and `defaults` must be derived from that same list.

The current page renders the same rows into both the inspector and a modal. A
save can therefore read the hidden modal, whose checkbox still has the old
checked value. The editor must use one authoritative DOM source per open
editor (the visible modal is recommended), or copy the visible source into the
other view before reading. `readFieldEditor` must query that source and return
only rows whose checkbox is currently checked. Never merge the old persisted
fields back into the result for the edited node.

On save, replace all fields for the selected node with the checked rows and
retain fields for other nodes. An empty checked set is a valid update and must
delete every field for that node. Save the derived workflow config and canvas
document as one logical operation; if either request fails, keep the in-memory
state dirty and show the error rather than silently restoring the old list.
After success, update the selected workflow object and the list summary from
the saved state. The list count is the number of `CanvasDocument.Fields`, not a
stale server-side `field_count`.

Canonical field shape:

```json
{
  "id": "2::text",
  "node_id": "2",
  "input": "text",
  "name": "Prompt",
  "control": "textarea",
  "value_type": "string",
  "default": "",
  "exposed": true
}
```

When reading legacy config, accept `label` as an alias for `name`,
`value_type`/`type` as type aliases, and synthesize `id` as
`<node_id>::<input>`. The persisted representation should always use the
canonical keys above.

## 2. Run value mapping contract

Canvas fields are converted to regular workflow bindings immediately before
`ApplyBindings`:

```text
CanvasField(id=node::input) -> Binding{
  Key:  node::input,
  NodeID: node,
  Path: inputs.input,
  Type: value_type,
}
```

The backend must accept both the canonical key and compatibility keys so old
clients and the test canvas continue to work:

| Binding target | Accepted value keys (most specific first) |
| --- | --- |
| `2::text` | `2::text`, `field_values[2::text]`, `text` when unique |
| prompt field | canonical ID, `positive_prompt`, `prompt`, `text` when unique |
| any field | canonical ID, `node_id::input`, input name when unique |

Alias resolution is performed per binding, not by blindly renaming the whole
value map. An input-name alias is valid only when it identifies one exposed
field; if multiple fields have the same input name, the request must report an
ambiguous key instead of writing the wrong node. The prompt aliases should be
considered for fields whose input or label contains `prompt`/`text`, with the
usual `positive_prompt` preference over a generic `text` alias.

Value precedence is deterministic:

1. Explicit `field_values` canonical key.
2. Explicit `field_values` compatibility alias.
3. `parameters` canonical key, then its compatibility alias.
4. Request `prompt`/`negative_prompt` aliases (only for the matching prompt
   field).
5. Mini-test-card values.
6. Canvas field default.
7. Original workflow input value.

An explicit value of `false`, `0`, or an empty string is still present and must
not fall through to a default. Apply the declared `value_type` conversion
after alias resolution. The generated ComfyUI JSON must contain the converted
value at `workflow[node_id].inputs[input]`; this is the assertion to log in
debug/test mode.

## 3. Output and media response contract

Output classification must normalize MIME before choosing the kind:

1. Read the ComfyUI item MIME/content type.
2. If absent, infer MIME from the filename extension.
3. If MIME starts with `image/`, force `kind: "image"` and
   `previewable: true`, even when the filename/class type is generic and even
   when an older stored item says `kind: "file"`.
4. Apply the equivalent rules for video/audio; otherwise use `kind: "file"`.

Every job response should expose an `outputs` array. A legacy single `output`
object is accepted by the client and normalized into a one-item array. An
image item contains at least:

```json
{
  "kind": "image",
  "mime": "image/png",
  "previewable": true,
  "url": "/api/v2/comfyui/jobs/<job>/outputs/0",
  "preview_url": "/api/v2/comfyui/jobs/<job>/image"
}
```

`GET /api/v2/comfyui/jobs/{id}/outputs/{index}` is a media endpoint, not a
metadata envelope, whenever the selected output is image-like (`mime` starts
with `image/` or `previewable` is true). It must return the saved binary with
the real `Content-Type`, `Content-Length` when available, and a cache-safe
disposition. This rule applies even if a legacy record has `kind: "file"`.
Non-media metadata may retain the existing envelope response. The existing
`/jobs/{id}/image` URL remains a compatibility alias for the first saved image.
Missing files return a clear 404 and should not be rendered as a successful
image by the browser.

The frontend image predicate is:

```text
kind == image OR previewable == true OR mime starts with image/
  OR filename has a supported image extension
```

It must use `url`, then `preview_url`, then the indexed output URL, and display
a load-error state when the binary endpoint returns an error.

## 4. Regression matrix

| Area | Scenario | Expected result |
| --- | --- | --- |
| Field editor | Check one primitive input and save | One canonical field, one binding, list count 1 |
| Field editor | Uncheck the same input and save | Field, binding, default, node exposed ID removed; list count 0 |
| Field editor | Edit in inspector while modal is open | The visible editor state is the only state persisted |
| Persistence | Reload workflow after each change | Canvas and workflow config have identical fields |
| Run mapping | Submit `field_values[2::text]` | Node `2.inputs.text` contains the submitted text |
| Run mapping | Submit `positive_prompt` for a prompt field | Prompt node input receives it |
| Run mapping | Submit `0`, `false`, or empty string | Explicit value wins over default |
| Run mapping | Two nodes share `text`, submit generic `text` | Request is rejected as ambiguous; no node is changed |
| Output | ComfyUI item has `mime: image/png` but generic filename | `kind=image`, `previewable=true`, image renders |
| Output | Stored item is `kind=file`, `mime=image/png` | Indexed output endpoint returns PNG bytes, not JSON envelope |
| Output | Job has only legacy `output` object | Client normalizes it and renders the image |
| Output | Output file missing | Indexed endpoint returns 404; UI shows load error |
| Compatibility | Existing workflow/job/config routes | Route names and `{code,data,msg}` metadata remain unchanged |

## 5. Non-goals and API stability

This fix does not rename existing routes, alter ComfyUI API JSON, or remove
legacy `bindings`, `defaults`, `output`, or `outputs` fields. It only adds
canonical normalization and compatibility alias handling. Binary media endpoints
are the intentional exception to the JSON envelope rule because an `<img>`
element requires the image bytes and MIME directly.
