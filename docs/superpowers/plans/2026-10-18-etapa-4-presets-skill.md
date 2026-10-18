# fal-cli — Etapa 4: Presets + skill `fal-generate` — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `fal-cli run @preset` con los mismos nombres de preset que kie-cli, `preset list|show`, y la skill `fal-generate` embebida e instalable con `fal-cli skills install`, verificada contra el CLI por un test.

**Architecture:** Puerto directo de kie-cli: `internal/preset` (YAML embebido + `~/.fal-cli/presets.yaml`, merge por nombre), `internal/skills` (copia de un árbol `fs.FS` a un directorio), `assets.go` en la raíz del módulo con `go:embed presets/default.yaml skills`. `run` resuelve `@name` antes de `schema.Fetch` y mezcla `preset.Params < --input < -p`. La skill vive en `skills/fal-generate/` con `SKILL.md` + `references/{image,video,audio}.md`.

**Tech Stack:** Go 1.27, cobra, `gopkg.in/yaml.v3` (nueva dependencia, la misma que kie-cli).

**Spec:** `docs/superpowers/specs/2026-10-17-fal-cli-design.md` (§4 interfaz y reglas de `run`, §5 presets, §6 skill).

## Global Constraints

- Contrato JSON y códigos de salida sin cambios. Con preset la salida de `run` (dry-run, `--no-wait` y éxito) lleva la clave `"preset"`.
- Precedencia de input: preset < `--input` < `-p`.
- Nombres de preset idénticos a kie-cli: `imagen-rapida`, `imagen-calidad`, `imagen-edicion`, `video-calidad`, `video-imagen`, `video-inicio-fin`, `video-barato`, `musica`, `instrumental`, `voz`.
- Los params de cada preset usan los nombres y tipos exactos del schema de fal (fixtures en `testdata/live/schemas/`).
- La skill **no** contiene lógica de fallback kie→fal.
- La key nunca llega a stdout/stderr. Sin red real en tests. Cobertura ≥80% por paquete; `go test -race ./...`, `go vet`, `gofmt -l` limpios antes de cada commit.

## Review Focus

1. **Preset con campo requerido no cubierto** (`run @voz` sin `text`): error `validation` con `details.problems` nombrando `text`; el schema manda, no el preset. → test en Task 2.
2. **Preset de usuario que sobrescribe un nombre de fábrica** (`~/.fal-cli/presets.yaml` con `imagen-rapida` → otro modelo): `preset list` lo muestra con `source: user` y `run @imagen-rapida` usa el modelo del usuario. → test en Task 2.
3. **`-p` sobre un param del preset** (`run @video-calidad -p duration=10`): gana `-p`, el body lleva `"10"` (string según schema). → test en Task 2.
4. **`skills install` sobre una instalación previa**: sobrescribe `fal-generate/**` y no toca otras skills del directorio. → test en Task 3 (paquete `skills`).
5. **Skill que menciona un comando o flag inexistente**: el test de consistencia falla nombrando el archivo y el token. → test en Task 3.

---

### Task 1: Paquete `preset` y presets de fábrica

**Files:**
- Create: `assets.go` (package `falcli`), `presets/default.yaml`, `internal/preset/preset.go`, `internal/preset/preset_test.go`
- Modify: `go.mod` (`gopkg.in/yaml.v3`)

**Interfaces:**
- Produces:
  - `assets.go`: `//go:embed presets/default.yaml skills` → `var Assets embed.FS` (package `falcli`). Hasta la Task 3 no existe `skills/`: embeber solo `presets/default.yaml` en esta tarea y ampliar el patrón en la Task 3.
  - `preset.Preset{Name, Description, Model string; Params map[string]any; Source string}` (json tags `name, description, model, params(omitempty), source`), constantes `SourceFactory = "factory"`, `SourceUser = "user"`.
  - `preset.Parse(data []byte, source string) ([]Preset, error)`: YAML `presets: [{name, description, model, params}]`, `KnownFields(true)`, nombre `^[a-z0-9][a-z0-9-]*$`, sin duplicados, `model` obligatorio, params normalizados vía JSON con `UseNumber` (números → `json.Number`, `nil` si vacío).
  - `preset.Load(factory []byte, userPath string) ([]Preset, error)`: merge por nombre (usuario reemplaza el preset entero), ordenado por nombre; archivo de usuario ausente no es error; otros errores de lectura/parse se devuelven con el path.
  - `preset.Find(list []Preset, name string) (Preset, bool)`.
  - `presets/default.yaml` con exactamente estos 10 presets (descripción en inglés, estilo kie: qué es, precio orientativo, "Missing fields - …"):

| name | model | params | Missing fields |
|---|---|---|---|
| imagen-rapida | `fal-ai/flux/schnell` | `image_size: square_hd`, `output_format: jpeg` | prompt |
| imagen-calidad | `fal-ai/nano-banana-pro` | `aspect_ratio: "1:1"`, `resolution: "1K"`, `output_format: png` | prompt |
| imagen-edicion | `fal-ai/nano-banana-2/edit` | `output_format: png` | prompt, image_urls |
| video-calidad | `fal-ai/kling-video/v2.5-turbo/pro/text-to-video` | `duration: "5"`, `aspect_ratio: "16:9"` | prompt |
| video-imagen | `fal-ai/kling-video/v2.5-turbo/pro/image-to-video` | `duration: "5"` | prompt, image_url |
| video-inicio-fin | `fal-ai/kling-video/v2.5-turbo/pro/image-to-video` | `duration: "5"` | prompt, image_url, tail_image_url |
| video-barato | `minimax/h3-max-turbo/text-to-video` | `resolution: 480P`, `duration: 5`, `prompt_expansion_mode: balanced` | prompt |
| musica | `fal-ai/minimax-music/v2.6` | `is_instrumental: false` | prompt (style); optional lyrics |
| instrumental | `fal-ai/minimax-music/v2.6` | `is_instrumental: true` | prompt |
| voz | `fal-ai/elevenlabs/tts/multilingual-v2` | `voice: Rachel` | text |

  Precios para las descripciones (spec §5): schnell 0.003/megapixel, nano-banana-pro 0.15/image, nano-banana-2/edit 0.08/image, kling 0.07/s (≈0.35 per 5 s), h3-max-turbo 0.016/s (≈0.08 per 5 s), minimax-music 0.15/track, elevenlabs 0.10 per 1000 characters.

- [ ] **Step 1: Write the failing tests** (`internal/preset/preset_test.go`):

```go
func TestParseNormalizesNumbersAndSource(t *testing.T) // yaml con params {n: 5, f: 1.5, s: "x", b: true} → Params["n"] == json.Number("5"), Source == "factory"; params vacíos → nil
func TestParseRejectsBadNames(t *testing.T)           // "Bad Name", "", duplicado, model vacío, clave desconocida (KnownFields) → error que contiene el nombre/índice
func TestLoadUserOverridesByName(t *testing.T)        // factory {a: m1, b: m2}, user {b: m3 sin params} → b.Model == m3, b.Params == nil, b.Source == "user", orden a,b; user ausente → solo factory; user malformado → error con el path
func TestFactoryPresetsLoad(t *testing.T)             // os.ReadFile("../../presets/default.yaml") → 10 presets, nombres exactos de la tabla en orden alfabético, cada Model con fixture en testdata/live/schemas/
```

- [ ] **Step 2: Run** `go test ./internal/preset/`. Expected: FAIL (package missing).
- [ ] **Step 3: Implement** `internal/preset/preset.go` (puerto de kie-cli), `presets/default.yaml`, `assets.go`, `go get gopkg.in/yaml.v3`.
- [ ] **Step 4: Run** `go test -race -cover ./internal/preset/ && go build ./...`. Expected: PASS ≥80%.
- [ ] **Step 5: Commit** `feat: preset package and factory presets with kie-cli names`

---

### Task 2: `preset list|show` y `run @preset`

**Files:**
- Create: `internal/cli/preset.go`, `internal/cli/preset_test.go`
- Modify: `internal/cli/root.go`, `internal/cli/run.go`, `internal/cli/run_test.go`

**Interfaces:**
- Consumes: `preset.Load/Find`, `falcli.Assets`, `config.Paths.Presets`.
- Produces:
  - `fal-cli preset list` → `{"ok":true,"presets":[{name,description,model,source}…]}` ordenado por nombre. `fal-cli preset show <name>` (con o sin `@`) → `{"ok":true,"preset":{name,description,model,params,source}}`; desconocido → `usage` "unknown preset @<name>; see fal-cli preset list". Fallo de parse del archivo de usuario → `usage` con el mensaje de `preset.Load`.
  - `run`: si el argumento empieza por `@`, `lookupPreset`; el endpoint es `p.Model`; `buildInput` mezcla `MergeInput(MergeInput(p.Params, base), over)`; `preset` en las salidas de dry-run, `--no-wait` y éxito (`withPreset(out, name)`; sin preset no hay clave). `finishArgs` gana `preset string`; `task wait` nunca lo rellena.
  - Help de `run`: `Use: "run <endpoint|@preset>"`.

- [ ] **Step 1: Write the failing tests**:

```go
func TestPresetListCommand(t *testing.T)            // 10 presets, [0].name == "imagen-calidad", todos source "factory", sin clave params
func TestPresetShowAcceptsAt(t *testing.T)          // show @voz → preset.model == "fal-ai/elevenlabs/tts/multilingual-v2", params.voice == "Rachel"; show nope → exit 2 usage
func TestPresetUserOverride(t *testing.T)           // FAL_CLI_HOME con presets.yaml {imagen-rapida → fal-ai/flux/dev} → list muestra source user y model flux/dev; run @imagen-rapida -p prompt=x --dry-run → m["model"] == "fal-ai/flux/dev"
func TestRunPresetMergesParamsAndTagsOutput(t *testing.T) // run @imagen-rapida -p prompt=fox (newFal) → submit body image_size == "square_hd", output_format == "jpeg", prompt == "fox"; m["preset"] == "imagen-rapida"; --no-wait también lleva preset
func TestRunPresetParamOverride(t *testing.T)       // run @video-calidad -p prompt=x -p duration=10 --dry-run → input.duration == "10", aspect_ratio == "16:9"
func TestRunPresetMissingRequiredIsValidation(t *testing.T) // run @voz --dry-run (sin text) → exit 2 validation, problems[].field == "text"
func TestRunUnknownPresetIsUsage(t *testing.T)      // run @nope → exit 2, message contiene "preset list"
func TestFactoryPresetsDryRunClean(t *testing.T)    // para cada preset: run @name --dry-run con los "Missing fields" rellenos (prompt=x, text=x, image_url(s)=https://example.com/a.png, tail_image_url igual) → exit 0, m["preset"] == name, m["model"] == modelo de la tabla, sin clave "problems"
```

- [ ] **Step 2: Run** `go test ./internal/cli/`. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `go test -race -cover ./...`. Expected: PASS ≥80%.
- [ ] **Step 5: Commit** `feat: preset list/show and run @preset with kie-cli names`

---

### Task 3: Skill `fal-generate` y `skills install`

**Files:**
- Create: `internal/skills/skills.go`, `internal/skills/skills_test.go`, `internal/skills/skills_unix_test.go`, `internal/cli/skills.go`, `internal/cli/skills_consistency_test.go`, `skills/fal-generate/SKILL.md`, `skills/fal-generate/references/image.md`, `skills/fal-generate/references/video.md`, `skills/fal-generate/references/audio.md`
- Modify: `assets.go` (`//go:embed presets/default.yaml skills`), `internal/cli/root.go`

**Interfaces:**
- Produces:
  - `skills.Install(src fs.FS, root, target string) ([]string, error)`: copia `root/**` a `target/<base(root)>/**` (dirs 0755, files 0644), sobrescribe, devuelve rutas escritas ordenadas; root ausente o target que es un fichero → error.
  - `fal-cli skills install [--target DIR]`: default `<home>/.claude/skills` (`config.UserHome`); sin home y sin `--target` → `usage` "cannot find the home directory: set HOME or USERPROFILE, or pass --target"; salida `{"ok":true,"target":…,"files":[…]}`; error de copia → `io`.
  - `skills/fal-generate/SKILL.md`: frontmatter `name: fal-generate`, `description:` (cuándo usarla: generar/editar imagen, video, música, voz con fal.ai vía `fal-cli`); secciones Flow (search/list → `models schema` → `models price` → `--dry-run` si es caro → `run` → `files[].path`), Passing input, Presets (tabla de los 10 con "Missing fields"), Output (`requestId`, `files`, `ids` con `request_id`/`seed`/`duration`/`<key>_url(s)`, `result`, `inferenceTime`, `uploaded`, `preset`), Rules, Errors (tabla con los 10 códigos y exits; `submit_uncertain` → comprobar https://fal.ai/dashboard/requests), Other commands (`task cancel`, `auth status`, `skills install`, `--quiet`, `--debug`), References. Avisos propios de fal (spec §6): el resultado expira minutos tras completarse (`--no-wait` solo si se hará `task wait` enseguida); `credits` puede fallar por permisos de la key (no bloqueante); el precio es unitario y el total depende del output; un 422 del servidor puede haberse cobrado. **Sin sección de fallback.**
  - `references/image.md` (flux schnell/dev, nano-banana-pro/-2 y edits con `image_urls`, gpt-image, `image_size` vs `aspect_ratio`, `num_images`), `references/video.md` (kling t2v/i2v/`tail_image_url`, veo3.1 fast first-last-frame, minimax h3, costes por segundo, `--no-wait` + `task wait`, `task cancel`), `references/audio.md` (minimax-music v2.6 `prompt`+`lyrics`+`is_instrumental`, music-3, elevenlabs tts `voice`, demucs stems en `ids.vocals_url`…, recovery: `task get/wait`, `task list`, `submit_uncertain`). Todos los ejemplos usan endpoints con fixture en `testdata/live/schemas/` y campos que existen en esos schemas.
  - Tests de consistencia (puerto de kie-cli con prefijo `fal-cli `): `TestCheckSkillTextCatchesBadRefs`, `TestSkillsMentionOnlyRealCommands` (≥4 archivos .md embebidos), `TestSkillDocumentsExitCodes` (los 10 códigos), `TestSkillSubmitUncertainPointsToDashboard` ("fal.ai/dashboard/requests"), `TestSkillFrontmatter`, y nuevo `TestSkillHasNoFallbackSection` (ningún .md contiene "fallback" ni "kie-cli").

- [ ] **Step 1: Write the failing tests** (`internal/skills/*_test.go` = copia de kie-cli con import de fal-cli; `internal/cli/skills_consistency_test.go` como arriba; `internal/cli/skills_test.go`):

```go
func TestSkillsInstallToTarget(t *testing.T)   // skills install --target <tmp> → exit 0, files contiene <tmp>/fal-generate/SKILL.md y references/{image,video,audio}.md
func TestSkillsInstallNoHomeIsUsage(t *testing.T) // Env sin HOME/USERPROFILE → exit 2 usage "pass --target"
```

- [ ] **Step 2: Run** `go test ./internal/skills/ ./internal/cli/`. Expected: FAIL.
- [ ] **Step 3: Implement** package, command, skill docs; ampliar `assets.go`.
- [ ] **Step 4: Run** `go test -race -cover ./... && make build && bin/fal-cli skills install --target /tmp/x >/dev/null`. Expected: PASS ≥80%; install escribe 4 archivos.
- [ ] **Step 5: Commit** `feat: fal-generate skill bundled and installable with skills install`

---

## Fin de la etapa 4

Entregable: presets con nombres de kie-cli validados contra los schemas reales, `run @preset`, skill `fal-generate` embebida, instalable y verificada contra el CLI. Pendiente: distribución (5), verificación real (6).
