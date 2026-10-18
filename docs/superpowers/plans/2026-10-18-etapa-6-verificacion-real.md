# fal-cli — Etapa 6: Verificación real — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Comprobar contra la API real que el catálogo completo y todos sus schemas pasan por `Summarize`/`SchemaFileKey`/`Validate` sin romperse, ejecutar los smoke tests pagados dentro de `FAL_E2E_BUDGET` (2 USD), corregir lo que falle y refrescar fixtures.

**Architecture:** Dos test files con build tag `live` que solo corren con `FAL_E2E=1` y la key en `FAL_API_KEY`: `internal/cli/live_free_test.go` (0 USD: catálogo + schemas de todos los endpoints activos de las categorías de media) y `internal/cli/live_paid_test.go` (presupuesto; cada generación consulta `models price` antes y se salta si no cabe). Nada de esto corre en CI (guardado por `workflows_test.go`). Los hallazgos se corrigen con TDD contra fixtures; `scripts/capture` refresca `testdata/live/`.

**Tech Stack:** Go 1.27, `go test -tags live`.

**Spec:** `docs/superpowers/specs/2026-10-17-fal-cli-design.md` (§8 etapa 6, §9 smoke reales, §10 riesgos).

## Global Constraints

- La key se carga solo en el shell (`set -a; . ./.env; set +a`) y se lee de `FAL_API_KEY`; nunca se imprime, nunca se escribe en fixtures ni en logs de test. Los outputs capturados se sanean (sin headers, sin query strings firmadas).
- Presupuesto: `FAL_E2E_BUDGET` (USD, defecto `2`). Antes de cada generación pagada: `models price`; coste estimado = `unit_price × unidades previstas` (imagen: 1 imagen o megapíxeles de `image_size`; video: segundos; tts: caracteres/1000; música: 1 track). Si `gastado + estimado > presupuesto` → `t.Skip` con el motivo. Suma acumulada en un contador del proceso.
- Nunca se ejecuta `bin/fal-cli run` sin `--dry-run` fuera de estos tests.
- Todo arreglo de código nace de un test contra fixtures (RED→GREEN); los tests `live` solo detectan.
- `go test -race ./...` (sin tag) sigue verde y ≥80% por paquete.

## Review Focus

1. **Schema real que rompe `Summarize` o `Resolve`** (ref externo, `allOf`, `type` como array): el test free lo lista por endpoint y no aborta el barrido. → Task 1.
2. **Campo de archivo no detectado** en algún schema activo (nombre sin `_url` y sin `ui.field`): el informe lista los candidatos (`description` con "URL"/"file" y sin detección) para decidir en Task 3. → Task 1.
3. **Forma real del error de saldo**: si el saldo lo permite no se puede provocar; el plan lo deja documentado como no verificado salvo que aparezca. → Task 3 (nota en ledger).
4. **Resultado expirado** tras `--no-wait`: `task wait` del request pagado dentro de 1 minuto debe descargar; si expira, `task_failed` con mensaje "result expired". → Task 2.
5. **422 del servidor con body no documentado**: `run` pagado con un valor fuera de enum bajo `--no-validate` → `validation` con `details.problems` (se cobra 0: rechazo previo). → Task 2.

---

### Task 1: Barrido gratuito del catálogo y los schemas

**Files:**
- Create: `internal/cli/live_free_test.go` (`//go:build live`)

**Interfaces:**
- Produces:
  - Helper `liveEnv(t)`: `t.Skip` si `FAL_E2E != "1"` o `FAL_API_KEY` vacío; devuelve `Deps` con `FAL_CLI_HOME` temporal y el entorno real; `liveRun(t, args...) (int, map[string]any, string)`.
  - `TestLiveFreeCatalogAndSchemas`: pagina `models list --category <c> --limit 100 --cursor` para las 8 categorías de media hasta `hasMore=false`; para cada `endpoint_id` llama `models schema <id>` (concurrencia 8, timeout global 15 min); cuenta: ok, `validation` (404), `api`; para cada ok comprueba `required`, `fields`, `output` presentes. Informe en `t.Logf` + escribe `testdata/live/REPORT-sweep.md` (contadores, lista de endpoints fallidos con el código, lista de campos "sospechosos": `type: string` con `description` que contenga `url`/`file`/`image`/`audio`/`video` y que `upload.SchemaFileKey` no detecte). Falla solo si `ok == 0` o si algún schema devuelve `api` por decode/resolve (no por red).
  - `TestLiveFreeDryRunPresets`: para los 10 presets, `run @name --dry-run` con los campos faltantes rellenos (mismo mapa que `TestFactoryPresetsDryRunClean`) → exit 0 y `price` no nulo.
  - `TestLiveFreeCreditsShape`: `credits` → exit 3 `auth` con "BILLING" (key actual) o exit 0 con `balance` numérico (key con BILLING).

- [ ] **Step 1: Write the tests** (son `live`; sin fase RED: documentar en el ledger que detectan, no prueban código nuevo).
- [ ] **Step 2: Run** `set -a; . ./.env; set +a; FAL_E2E=1 go test -tags live -run TestLiveFree -v -timeout 20m ./internal/cli/ 2>&1 | tail -40`. Expected: PASS o lista concreta de fallos para la Task 3. Leer `testdata/live/REPORT-sweep.md`.
- [ ] **Step 3: Commit** `test: live free sweep of the catalog, schemas, presets and credits`

---

### Task 2: Smoke tests pagados dentro del presupuesto

**Files:**
- Create: `internal/cli/live_paid_test.go` (`//go:build live`)

**Interfaces:**
- Produces:
  - `budget` (package-level, `sync.Mutex`, `limit` de `FAL_E2E_BUDGET` o 2, `spent`); `reserve(t, endpoint, units float64) ` llama `models price`, calcula el coste, `t.Skipf` si no cabe, suma si cabe y `t.Logf("cost ≈ %.4f USD (%s)", …)`.
  - `capture(t, name, m)`: guarda `m["result"]` saneado en `testdata/live/results/<name>.json` (solo payload crudo; sin headers).
  - Tests, en orden de coste:
    1. `TestLivePaid1Image`: `run @imagen-rapida -p prompt="a red fox, watercolor" -p image_size=square --out <tmp>` (≈0.003) → `files[0].kind == image`, fichero existe, `ids.images_url`, `ids.seed`.
    2. `TestLivePaid2Voice`: `run @voz -p text="Hola desde fal" --out <tmp>` (≈0.0015) → `files[0].kind == audio`, `ids.audio_url`.
    3. `TestLivePaid3EditUploadChain`: `run @imagen-edicion -p prompt="make it night" -p image_urls=<fichero del test 1> --out <tmp>` (0.08) → `uploaded` tiene 1 URL `fal.media`, `files[0]`.
    4. `TestLivePaid4NoWaitThenWait`: `run fal-ai/flux/schnell -p prompt=... --no-wait` → `state submitted`, stderr "expires"; `task get <id>` → estado válido; `task wait <id> --out <tmp>` → `success` con ficheros; `task list` lo muestra `success` (≈0.003).
    5. `TestLivePaid5Music`: `run @musica -p prompt="lofi, rainy night" -p lyrics="[verse] rain on the window" --out <tmp>` (0.15) → audio.
    6. `TestLivePaid6CheapVideo`: `run @video-barato -p prompt="a drone shot over a misty forest" --out <tmp> --timeout 15m` (≈0.08) → `files[0].kind == video`, `ids.video_url`, `ids.duration` si lo trae.
    7. `TestLivePaid7ServerValidation`: `run fal-ai/flux/schnell -p prompt=x -p num_images=99 --no-validate` → exit 2 `validation` con `details.problems` (0 USD esperado; si fal lo acepta y cobra, `reserve` lo contabiliza como 1 imagen antes).
    8. `TestLivePaid8CancelQueued`: `run @video-barato -p prompt=x --no-wait` → `task cancel <id>` → `CANCELLATION_REQUESTED` o `ALREADY_COMPLETED`; reserva el coste del video por si no cancela a tiempo; se salta si no cabe.

- [ ] **Step 1: Write the tests.**
- [ ] **Step 2: Run** `set -a; . ./.env; set +a; FAL_E2E=1 FAL_E2E_BUDGET=2 go test -tags live -run TestLivePaid -v -timeout 40m -p 1 ./internal/cli/ 2>&1 | tail -60`. Expected: PASS/SKIP con el gasto acumulado en el log; ningún FAIL sin explicación.
- [ ] **Step 3: Commit** `test: budgeted live smoke tests against the fal queue`

---

### Task 3: Correcciones y refresco de fixtures

**Files:**
- Modify: lo que señalen las Tasks 1–2 (`internal/schema`, `internal/upload`, `internal/result`, `internal/task`, `presets/default.yaml`, skill docs), `testdata/live/**`, `testdata/live/REPORT.md`

**Interfaces:**
- Produces: un test contra fixtures por hallazgo (RED→GREEN), fixtures refrescados con `go run ./scripts/capture` (schemas nuevos para cualquier endpoint que los tests live hayan usado y no estuviera), `result.Extract` recalibrado si algún resultado real en `testdata/live/results/` no se extrae (añadir esos payloads a `TestExtractFindsEveryFileInCapturedOutputs` como fixtures literales), ledger de la etapa con "no verificado: forma del error de saldo" si no apareció.

- [ ] **Step 1:** Por cada hallazgo: test RED con fixture → fix → GREEN.
- [ ] **Step 2: Run** `set -a; . ./.env; set +a; go run ./scripts/capture -out testdata/live && go test -race -cover ./... && FAL_E2E=1 go test -tags live -run TestLiveFree -timeout 20m ./internal/cli/`. Expected: PASS ≥80%; sweep limpio.
- [ ] **Step 3: Commit** `fix: findings from the live verification and refreshed fixtures` (o `test:` si solo hay fixtures).

---

## Fin de la etapa 6

Entregable: CLI verificado contra la API real, fixtures al día, gasto registrado en el ledger. Pendiente del proyecto: solo subir a GitHub (sin remoto todavía).
