---
name: fal-generate
description: Generate or edit images, video, music, speech and sound effects with fal.ai through the `fal-cli` tool. Use when the user asks to generate/edit an image, make a video (text-to-video, image-to-video, first/last frame), compose a song or instrumental, synthesize speech, or create sound, using fal.ai models.
---

# fal-generate

Drive fal.ai (image, video, audio models) with the `fal-cli` binary. Every command prints exactly one JSON object on stdout; progress and warnings go to stderr. Parse stdout, ignore stderr.

This skill is guidance only. The model's own schema is the source of truth: never invent models or fields.

## Flow

1. Find a model: `fal-cli models search <text>` or `fal-cli models list --type video` (`--type` image|video|audio, or `--category text-to-video` for a native category). Ids look like `fal-ai/flux/schnell`, `fal-ai/kling-video/v2.5-turbo/pro/text-to-video`, `minimax/music-3`. Pages: `--limit 50`, continue with `--cursor <nextCursor>`.
2. Read its input: `fal-cli models schema <endpoint>`. Use only the fields it lists (`required`, `fields` with type/enum/default/min/max, `output`). `--refresh` bypasses the 24h cache; `--raw` gives the full OpenAPI document.
3. Check cost when it matters (always for video): `fal-cli models price <endpoint>` gives `{unit_price, unit, currency}`. The price is per unit (megapixel, second, image, 1000 characters...): the total depends on the output you ask for. `fal-cli credits` shows the balance only when the key has the BILLING permission; an `auth` error there is not blocking, just skip it.
4. Optional rehearsal: `fal-cli run <endpoint> -p prompt=... --dry-run` validates the input, prints the request body, the pending uploads and the price, and submits nothing.
5. Generate: `fal-cli run fal-ai/flux/schnell -p prompt="a red fox" -p image_size=landscape_16_9 --out ./media`.
6. Use the files listed in `files[].path`.

## Passing input

- `-p key=value`, repeatable. Values are typed against the schema (numbers, booleans, JSON `{...}`/`[...]`).
- A repeated key builds an array: `-p image_urls=a.png -p image_urls=b.png`. Commas are never split (`image_urls=a,b` is ONE value), so always repeat `-p` for arrays.
- A local file path in a media field (`image_url`, `image_urls`, `audio_url`, `video_url`, fields the schema marks as image/video/audio/file) is uploaded to fal storage automatically and replaced by its URL; URLs and `data:` values pass through. Stderr says `uploading <path>` before each upload.
- `-p key=@file.txt` reads the text of a file into the value (for long prompts). `@@x` is a literal `@x`.
- `--input file.json` (or `--input -` for stdin) supplies a JSON object; `-p` overrides it.
- A rejected input fails with `validation` and `error.details.problems` (field, message, got). Fix the fields. `--no-validate` sends anyway (problems become warnings); only use it when the schema is clearly wrong. The server can still reject with a 422 `validation` error.

## Presets

Presets are named model + default params. `fal-cli preset list` shows them (`name`, `description`, `model`, `source`); `fal-cli preset show <name>` shows the params.

`fal-cli run @musica -p prompt="lofi, rainy night"` runs the preset. Precedence: preset params, then `--input`, then `-p`. The description says which fields you must still pass. The output has a `"preset"` key. Users can add or override presets in `~/.fal-cli/presets.yaml`.

| preset | model | pass |
|---|---|---|
| `imagen-rapida` | fal-ai/flux/schnell (0.003 USD per megapixel, about 0.003 per 1024x1024 image) | `prompt` |
| `imagen-calidad` | fal-ai/nano-banana-pro (0.15 USD per image) | `prompt` |
| `imagen-edicion` | fal-ai/nano-banana-2/edit (0.08 USD per image) | `prompt`, `image_urls` |
| `video-calidad` | Kling 2.5 turbo pro text-to-video, 5 s (about 0.35 USD) | `prompt` |
| `video-imagen` | Kling 2.5 turbo pro image-to-video, 5 s (about 0.35 USD) | `prompt`, `image_url` |
| `video-inicio-fin` | same model with first and last frame | `prompt`, `image_url`, `tail_image_url` |
| `video-barato` | minimax/h3-max-turbo/text-to-video, 480P, 5 s (about 0.08 USD) | `prompt` |
| `musica` | fal-ai/minimax-music/v2.6 (0.15 USD per track) | `prompt`, optional `lyrics` |
| `instrumental` | same model without vocals | `prompt` |
| `voz` | fal-ai/elevenlabs/tts/multilingual-v2 (0.10 USD per 1000 characters) | `text` |

## Output

`fal-cli run` submits, waits and downloads. Success looks like:

- `requestId`, `model`, `state` (`success`), `preset` (only with `@name`).
- `files`: list of `{path, url, kind}`. `path` is the local file (`<model>_<requestId>_<n>.<ext>`, never overwritten).
- `ids`: `request_id`, plus `seed` and `duration` when the result has them, and one URL per media key: `images_url` (one) or `images_urls` (several), `video_url`, `audio_url`, `vocals_url`... Use them to chain (see the references).
- `result` (raw API response), `inferenceTime`, `uploaded` (URLs of the files uploaded for this run).

Special cases:

- `--no-wait` returns at once with `requestId`, `state: "submitted"`, `ids.request_id`. The result expires minutes after completion: only use `--no-wait` when you will run `fal-cli task wait <requestId>` right away (same `--out`, `--no-download`, `--timeout`). Peek with `fal-cli task get <requestId>` (state, queue position, result when completed; no download).
- `--out DIR` sets the download directory (default: config `out_dir`, else `./fal-output`). `--no-download` skips downloading; the URLs stay in `result` and `ids`.
- `--timeout 20m` caps the wait (default 10m, 30m for video models).
- `fal-cli task list [--limit N]` shows recent local requests (newest first) with their state, useful to recover a requestId. `fal-cli task cancel <requestId>` drops a queued request (`CANCELLATION_REQUESTED`, or `ALREADY_COMPLETED` when too late).
- `task get`, `task wait` and `task cancel` take the endpoint from the local ledger; for a request made elsewhere pass `--model <endpoint>`.

## Rules

- Never invent an endpoint id or a field. If it is not in `models search` / `models schema`, it does not exist.
- Check `models price` before video and before batches. The charge happens on the server even if a later step (download) fails, and a server-side 422 may already have been charged.
- Parallelize: submit several with `--no-wait`, then `fal-cli task wait <requestId>` for each, right away (results expire).
- Chain with `ids` and `files`, not with guessed values: a follow-up run takes `images_url` / `video_url` / a local `files[].path` from the previous output.
- Always set `--out` to a directory the user expects; report the paths you produced.
- Durations and some counts are strings in several schemas (`duration: "5"`). Trust the schema.
- Non-media categories (`llm`, `vision`, `speech-to-text`, `image-to-3d`, `json`, `video-to-text`) are hidden from `models list`/`search` unless you ask for them with `--category`.

## Errors

On failure stdout is `{"ok":false,"error":{"code","message","details"?}}` and the exit code is set:

| exit | error.code | meaning and what to do |
|---|---|---|
| 0 | - | success |
| 2 | `usage` | bad command, flag or preset (or no HOME for `skills install`). Re-read `--help`, fix the call |
| 2 | `validation` | input does not match the schema (local or server 422), or unknown model. Read `details.problems`, fix fields, retry |
| 3 | `auth` | missing/invalid API key, or the key lacks a permission (`credits` needs BILLING). Ask the user to run `fal-cli auth login` or set `FAL_API_KEY`. Check with `fal-cli auth status` |
| 4 | `credits` | not enough balance or the account is locked. Stop and tell the user; do not retry |
| 5 | `task_failed` | the model failed the request, or its result expired (`details.requestId`, `details.errorType`). Change the prompt/inputs; do not blindly resubmit |
| 6 | `timeout` | still running when the wait ended. `details.requestId` is kept: `fal-cli task wait <requestId>` with a larger `--timeout` |
| 7 | `rate_limit` | too many requests. Wait a bit, then retry |
| 8 | `submit_uncertain` | the submit may or may not have created a request. Do NOT resubmit (double charge). Tell the user the request may exist and ask them to check https://fal.ai/dashboard/requests; resubmit only with their confirmation. `fal-cli task list` shows the attempt as `state: unknown` without a requestId |
| 1 | `api` | other fal.ai/network error. Read `message`, retry once if transient |
| 1 | `io` | local file problem (download, write, read). Check paths and disk; `details.media` lists the result URLs and `details.files` what was already saved; `fal-cli task wait <requestId>` downloads again while the result lasts |

Errors after a request was created carry `details.requestId`: always keep it, the request is paid and recoverable with `task wait`/`task get` for a few minutes.

## Other commands

- `fal-cli auth status`: where the key comes from (redacted).
- `fal-cli skills install [--target DIR]`: install this skill (default `~/.claude/skills`).
- `fal-cli version`.
- `--quiet` silences stderr; `--debug` adds diagnostics.

## References (read the one you need)

- [references/image.md](references/image.md): text-to-image, edit.
- [references/video.md](references/video.md): text/image/first-last-frame video, chaining, cancel.
- [references/audio.md](references/audio.md): music, speech, stems, recovery.
