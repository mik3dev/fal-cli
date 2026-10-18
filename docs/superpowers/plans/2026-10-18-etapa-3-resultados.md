# fal-cli — Etapa 3: Resultados e ids — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** El extractor de resultados reconoce todos los outputs de los 47 schemas capturados, `ids` sirve para encadenar comandos, y el camino `submit_uncertain` / estados del ledger no miente al agente.

**Architecture:** Sin paquetes nuevos. `internal/result` se calibra contra los schemas `*Output` reales (test generativo: instanciar cada schema de salida y comprobar que `Extract` encuentra cada `File`). `internal/task` y `internal/cli` absorben los menores diferidos de la revisión de la etapa 1 que tocan estados y recuperación.

**Tech Stack:** Go 1.27, stdlib. Sin dependencias nuevas.

**Spec:** `docs/superpowers/specs/2026-10-17-fal-cli-design.md` (§3 filas "Salida de media" e "Historial", §4 "Reglas de `run`" y "Contrato de salida", §8 etapa 3).

## Global Constraints

- Contrato JSON y códigos de salida sin cambios (§4). `ids` siempre lleva `request_id`.
- Estados del ledger: `submitted | unknown | success | fail | timeout | cancelled`. Un error transitorio nunca cambia el estado (ya fijado en la etapa 1).
- La key nunca llega a stdout/stderr/mensajes.
- Nada de red real; fixtures de `testdata/live/schemas/` como única fuente de formas de output.
- Inmutabilidad: funciones puras devuelven copias.
- Cobertura ≥80% por paquete; `go test -race ./...`, `go vet`, `gofmt -l` limpios antes de cada commit.

## Review Focus

1. **Output con `File` sin `content_type`** (`{url, width, height}` o `{url, file_name}`): se extrae y descarga; la extensión sale de la URL o del header. → test en Task 1.
2. **Output con varios archivos sueltos** (demucs: `vocals`, `drums`, `bass`…): `files[]` lleva todos en orden del documento y `ids` tiene `vocals_url`, `drums_url`… → test en Task 1.
3. **Reloj local adelantado** al enviar: `recoverUncertain` sigue adoptando el único request del historial (tolerancia de 30 s). → test en Task 2.
4. **Status `COMPLETED` con `error` en `task get`**: la salida lleva `error` y `errorType` y no intenta el GET del resultado. → test en Task 3.
5. **Descarga fallida tras `COMPLETED`**: el ledger queda en `success` con `note: files not downloaded` y el error lleva `details.media` para reintentar con `task wait`. → test en Task 3.

---

### Task 1: Extractor calibrado contra todos los outputs

**Files:**
- Modify: `internal/result/result.go`, `internal/result/result_test.go`

**Interfaces:**
- Produces:
  - `fileObject` acepta `url` string más **cualquiera** de `content_type`, `file_name`, `file_size`, `width`, `height` (hoy exige `content_type` o `file_name`).
  - `result.IDs` añade `"duration"` (número) cuando el output lo trae en la raíz, además de `seed`. Sin más cambios de forma.
  - Test generativo `TestExtractFindsEveryFileInCapturedOutputs`: para cada fixture, resuelve el schema `*Output` con `schema.Resolve`, instancia un valor de ejemplo (`instantiate(schema) any`: objeto → cada propiedad instanciada; array → un elemento; string → `"https://v3.fal.media/files/x.bin"` si la clave es `url`, si no `"s"`; integer/number → 1; boolean → true; anyOf → primera rama no `null`; sin tipo con `properties` → objeto) y comprueba que `Extract` devuelve exactamente tantas `Media` como propiedades `File` (objetos con `url` + otra clave de File) existen en el schema, recorriendo también arrays. Imprime con `t.Logf` el total (espera ≥ 50 archivos en ≥ 40 fixtures).

- [ ] **Step 1: Write the failing tests**:

```go
func TestExtractFileWithoutContentType(t *testing.T) // `{"images":[{"url":"https://v3.fal.media/a.png","width":1,"height":1}],"thumb":{"url":"https://v3.fal.media/t.png","file_size":10}}` → 2 Media, keys images[0] y thumb; `{"link":{"url":"https://x/y"}}` → 0 (solo url no basta)
func TestExtractStems(t *testing.T)                  // demucs-like `{"vocals":{File},"drums":{File},"bass":{File}}` → 3 en orden vocals, drums, bass; IDs → vocals_url, drums_url, bass_url
func TestIDsDuration(t *testing.T)                   // `{"video":{File},"duration":5.2}` → ids["duration"] == 5.2; sin duration → ausente
func TestExtractFindsEveryFileInCapturedOutputs(t *testing.T) // generativo (arriba)
```

- [ ] **Step 2: Run** `go test ./internal/result/`. Expected: FAIL (`FileWithoutContentType` → 0 media; generativo falla en flux `images` si el instanciador omite `content_type`… nota: el instanciador debe incluir todas las propiedades, así que el generativo falla solo si `fileObject` no reconoce alguna forma; comprobar que al menos el primer test falla).
- [ ] **Step 3: Implement** (`fileObject` ampliado; `duration` en `IDs`).
- [ ] **Step 4: Run** `go test -race -cover ./internal/result/`. Expected: PASS ≥80%.
- [ ] **Step 5: Commit** `feat: extract every File-shaped output and expose duration in ids`

---

### Task 2: `submit_uncertain` y transitorios

**Files:**
- Modify: `internal/task/task.go`, `internal/task/task_test.go`, `internal/cli/run.go`, `internal/cli/run_test.go`, `internal/cli/task.go`, `internal/cli/task_test.go`

**Interfaces:**
- Produces:
  - `recoverUncertain(ctx, c, endpoint, since)`: consulta con `start = since - 30s` (`skewTolerance`) y acepta `sent_at >= since - 30s`. Regla del candidato único sin cambios.
  - `task.GetResult`: un 2xx cuyo body no decodifica (`*api.Error` con `Status < 400`) se devuelve tal cual (código `api`, reintentable), no `task_failed`.
  - `record(path, e, warn)` pasa a no devolver nada (`func record(...)`), y `Submit` devuelve `id, nil` tras registrar.
  - `fal-cli run --no-wait` escribe en stderr `note: result expires minutes after completion; download now with: fal-cli task wait <id>` (misma constante `expiryWarning` + sufijo con el id).
  - `task cancel`: un fallo al anotar el ledger avisa en stderr (`warning: could not record request <id> in the ledger: …`) en vez de ignorarse.

- [ ] **Step 1: Write the failing tests**:

```go
func TestRecoverUncertainToleratesClockSkew(t *testing.T) // task_test: history con sent_at = t0 - 20s y submit 500 → se adopta; la query lleva start = t0-30s en RFC3339
func TestGetResultBadJSONIsAPIError(t *testing.T)         // result 200 body `{not json` → *api.Error Status 200, no output.Error task_failed
func TestRunNoWaitWarnsExpiry(t *testing.T)               // run_test: --no-wait → stderr contiene "expires" y "task wait req-1"
func TestTaskCancelLedgerWriteFailureWarns(t *testing.T)  // task_test (cli): FAL_CLI_HOME apuntando a un fichero (no dir) → exit 0 y stderr contiene "could not record"
```

  Ajustar `TestSubmit5xxAfterSendIsUncertainWhenHistoryEmpty`: el `start` esperado pasa a `2026-10-18T11:59:30Z`.

- [ ] **Step 2: Run** `go test ./internal/task/ ./internal/cli/`. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `go test -race -cover ./...`. Expected: PASS ≥80%.
- [ ] **Step 5: Commit** `fix: clock-skew tolerant uncertain-submit recovery and honest transient errors`

---

### Task 3: `task get` con error y ledger tras la descarga

**Files:**
- Modify: `internal/cli/task.go`, `internal/cli/run.go`, `internal/cli/task_test.go`, `internal/cli/run_test.go`

**Interfaces:**
- Produces:
  - `task get <id>` con `COMPLETED` y `Status.Error != ""` → `{"ok":true,"requestId","model","state":"COMPLETED","error":<msg>,"errorType":<type>}` sin llamar a `GetResult`.
  - `finish`: `recordFinal(success)` se ejecuta **después** de la descarga. Si la descarga falla: ledger `success` con `Note: "files not downloaded: <err>"`, y el error devuelto (código `io`) lleva `details.media` (ya) y `details.requestId`.
  - `catalog.Service.Find` se elimina (sin uso; `models schema` ya resuelve existencia). `asAPI` en `catalog` pasa a `errors.As`. `schema.Fetch` usa `errors.As`.

- [ ] **Step 1: Write the failing tests**:

```go
func TestTaskGetCompletedWithErrorSkipsResult(t *testing.T) // fakeQueue status `{"status":"COMPLETED","error":"nsfw","error_type":"content_policy_violation"}` y result que haría t.Fatal si se llama (handler cuenta llamadas) → m["error"]=="nsfw", m["errorType"], sin "result"
func TestRunDownloadFailureKeepsLedgerNote(t *testing.T)  // run_test: f.result apunta a /cdn/missing (404) → exit 1 code io, details.requestId; ledger success con Note que contiene "not downloaded"
```

  Borrar `TestFind*` de `catalog_test.go` al quitar `Find`.

- [ ] **Step 2: Run** `go test ./internal/cli/ ./internal/catalog/`. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `go test -race -cover ./...`, `make build`. Expected: PASS ≥80%.
- [ ] **Step 5: Commit** `fix: task get reports completed-with-error and ledger records success after download`

---

## Fin de la etapa 3

Entregable: extractor validado contra los 47 outputs capturados, `ids` con `request_id`/`seed`/`duration`/URLs por clave, recuperación tolerante a desfase de reloj y estados del ledger fieles. Pendiente: presets + skill (4), distribución (5), verificación real (6).
