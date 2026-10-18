# fal-cli

A CLI built for AI agents to generate images, video and audio through the [fal.ai](https://fal.ai) API. The models come from the live catalog (about 1200 endpoints). Every command prints exactly one JSON object on stdout; progress and warnings go to stderr. It ships an agent skill (`fal-generate`) that teaches agents how to drive it.

## Install

The repository is private, so every method needs access to `github.com/mik3dev/fal-cli`.

**(a) Release archive** (needs the [GitHub CLI](https://cli.github.com), logged in):

```sh
gh release download --pattern '*darwin_arm64*' --repo mik3dev/fal-cli
tar -xzf fal-cli_*_darwin_arm64.tar.gz   # Windows: unzip the .zip
# move the binary to a directory on your PATH
```

If macOS Gatekeeper blocks a browser-downloaded binary: `xattr -d com.apple.quarantine ./fal-cli`.

Archives are named `fal-cli_<version>_<os>_<arch>` (`tar.gz`, `zip` on Windows) for `linux`, `darwin` and `windows` on `amd64` and `arm64`. A `checksums.txt` is published with each release.

**(b) Go toolchain:**

```sh
GOPRIVATE=github.com/mik3dev/* go install github.com/mik3dev/fal-cli/cmd/fal-cli@latest
# PowerShell: $env:GOPRIVATE="github.com/mik3dev/*"; go install github.com/mik3dev/fal-cli/cmd/fal-cli@latest
```

A private repo also needs git credentials: run `gh auth setup-git`, or use an SSH rewrite (`git config --global url."git@github.com:".insteadOf "https://github.com/"`).

**(c) From a clone:**

```sh
make install
```

## Setup

```sh
fal-cli auth login    # prompts for the API key (or reads the first stdin line)
fal-cli auth status   # where the key comes from (redacted)
fal-cli credits       # balance; needs a key with the BILLING permission
```

Alternatively set `FAL_API_KEY` (or `FAL_KEY`); they take precedence over the stored key. Create keys at https://fal.ai/dashboard/keys; `credits` returns an `auth` error for a key without BILLING, everything else works. Config lives in `~/.fal-cli/` (`config.json`, `presets.yaml`, caches, `tasks.jsonl` ledger). Set `FAL_CLI_HOME` to use another directory. On Windows the home falls back to `USERPROFILE` when `HOME` is unset.

## Quickstart

```sh
fal-cli models search flux --type image            # find an endpoint
fal-cli models schema fal-ai/flux/schnell          # its input and output fields
fal-cli models price fal-ai/flux/schnell           # unit price
fal-cli run fal-ai/flux/schnell -p prompt="a red fox" --dry-run
fal-cli run fal-ai/flux/schnell -p prompt="a red fox" --out ./media
```

`--dry-run` validates the input against the endpoint schema, prints the request body, the pending uploads and the price, and submits nothing. A real `run` submits to the fal queue, waits and downloads. On success the output has `requestId`, `model`, `state`, `files`, `ids`, `result`, `inferenceTime`:

```json
{"ok":true,"requestId":"...","model":"fal-ai/flux/schnell","state":"success","files":[{"path":"media/fal-ai-flux-schnell_..._0.jpeg","url":"https://...","kind":"image"}],"ids":{"request_id":"...","seed":42,"images_url":"https://..."}}
```

Input rules: `-p key=value` is typed against the schema; repeat a key for arrays; a local file in a media field is uploaded to fal storage; `-p key=@file.txt` reads a file; `--input file.json` supplies the whole object. Results on fal expire minutes after completion, so `run` downloads by default; `--no-wait` returns the `requestId` and `fal-cli task wait <requestId>` collects it. `fal-cli task list`, `task get`, `task cancel` inspect and control requests recorded in the local ledger.

## Presets

A preset is a named model plus default params. Factory presets: `imagen-rapida`, `imagen-calidad`, `imagen-edicion`, `video-calidad`, `video-imagen`, `video-inicio-fin`, `video-barato`, `musica`, `instrumental`, `voz`.

| preset | model | orientative price (USD) |
|---|---|---|
| `imagen-rapida` | `fal-ai/flux/schnell` | 0.003 per megapixel |
| `imagen-calidad` | `fal-ai/nano-banana-pro` | 0.15 per image |
| `imagen-edicion` | `fal-ai/nano-banana-2/edit` | 0.08 per image |
| `video-calidad` | `fal-ai/kling-video/v2.5-turbo/pro/text-to-video` | 0.07 per second |
| `video-imagen` | `fal-ai/kling-video/v2.5-turbo/pro/image-to-video` | 0.07 per second |
| `video-inicio-fin` | same, with `image_url` + `tail_image_url` | 0.07 per second |
| `video-barato` | `minimax/h3-max-turbo/text-to-video` | 0.016 per second |
| `musica` / `instrumental` | `fal-ai/minimax-music/v2.6` | 0.15 per track |
| `voz` | `fal-ai/elevenlabs/tts/multilingual-v2` | 0.10 per 1000 characters |

```sh
fal-cli preset list
fal-cli preset show imagen-rapida
fal-cli run @imagen-rapida -p prompt="a red fox"
```

Precedence: preset params < `--input` < `-p`.

Add or override presets in `~/.fal-cli/presets.yaml`. A user preset with the same name replaces the factory one entirely:

```yaml
presets:
  - name: logo-video
    description: Short logo animation. Missing fields - prompt.
    model: fal-ai/kling-video/v2.5-turbo/pro/text-to-video
    params:
      duration: "5"
      aspect_ratio: "16:9"
```

Quote string values in YAML (for example `duration: "5"`): many schemas expect strings and an unquoted number would not match.

## Agent skill

```sh
fal-cli skills install                  # default: ~/.claude/skills
fal-cli skills install --target DIR
```

The `fal-generate` skill teaches an agent the flow (find an endpoint, read its schema, check the price, dry-run, run), how to pass input, how to use presets, how to chain requests with `ids`, how to read the output, and how to react to each error code. Deciding when to use fal.ai instead of another provider is the agent's job, not the skill's.

## Output and exit codes

Success prints one JSON object. Failure prints `{"ok":false,"error":{"code","message","details"?}}` and sets the exit code. Errors after a request was created carry `details.requestId`.

| exit | error.code | meaning |
|---|---|---|
| 0 | - | success |
| 2 | `usage` | bad command, flag or preset |
| 2 | `validation` | input does not match the schema (locally or a server 422), or unknown model |
| 3 | `auth` | missing or invalid API key, or a key without the needed permission |
| 4 | `credits` | not enough balance or locked account; do not retry |
| 5 | `task_failed` | the model failed the request, or its result expired |
| 6 | `timeout` | still running when the wait ended; the request id is kept for `fal-cli task wait` |
| 7 | `rate_limit` | too many requests; wait, then retry |
| 8 | `submit_uncertain` | the submit may or may not have created a request. Do not resubmit (double charge); check https://fal.ai/dashboard/requests |
| 1 | `api` | other fal.ai or network error |
| 1 | `io` | local file problem (download, write, read); `details.media` keeps the result URLs |

## Development

```sh
make test       # go test -race -cover ./...
make lint       # golangci-lint run ./...
make build      # bin/fal-cli
make snapshot   # local goreleaser build, no publish
make tidy-check # go mod tidy -diff (must print nothing)
```

Tests never call the real API: they run against `httptest` servers that replay the responses captured in `testdata/live/` (catalog, schemas, pricing). Refresh those fixtures with `go run ./scripts/capture` (free GET requests only). Live smoke tests run by hand only (build tag `live`, `FAL_E2E=1`) and never in CI. Load the key from a gitignored `.env` without printing it:

```sh
# free checks only (catalog, schemas, --dry-run; 0 USD)
set -a; . ./.env; set +a; FAL_E2E=1 go test -tags live -run TestLiveFree ./internal/cli/ -v
# paid smoke tests; FAL_E2E_BUDGET caps the USD spent in one run (default 2)
set -a; . ./.env; set +a; FAL_E2E=1 FAL_E2E_BUDGET=2 go test -tags live -run TestLivePaid -p 1 ./internal/cli/ -v
```

Releasing: push a tag `vX.Y.Z`. The release workflow runs goreleaser and publishes the archives and `checksums.txt` to GitHub Releases.
