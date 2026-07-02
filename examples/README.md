# grove examples

Copy-paste starting points for a project's `grove.jsonc` (drop one at the
project root, beside `.base`, and edit to taste). All files are JSONC, so `//`
and `/* */` comments and trailing commas are allowed. Validate against
[`../grove.schema.json`](../grove.schema.json) via the `$schema` reference.

| File | What it shows |
|------|---------------|
| [`grove.jsonc`](grove.jsonc) | The default starter `grove clone` seeds: branch colors + tmux, with commented-out `command`/`webhook` examples. |
| [`wsm-remote.jsonc`](wsm-remote.jsonc) | Remote workflow: a `webhook` recipe that POSTs to [wsm](https://github.com/KurtPreston/wsm) over a reverse SSH tunnel to open a window on your workstation. |

`grove.jsonc` is embedded into the grove binary (see [`examples.go`](examples.go))
and written verbatim by `grove clone`, so editing it here updates the seeded
starter too.
