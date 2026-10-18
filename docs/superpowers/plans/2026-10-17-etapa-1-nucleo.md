# fal-cli Etapa 1 (Núcleo) — Plan de implementación

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Binario `fal-cli` que autentica, explora el catálogo vivo de fal.ai, resume schemas, sube archivos locales, envía a la cola, espera, descarga resultados y cancela, con la misma salida JSON y códigos de salida que kie-cli.

**Architecture:** Paquetes pequeños en `internal/` (output, config, api, cache, catalog, schema, ledger, task, upload, result, download) y comandos cobra en `internal/cli/`. Seis paquetes se **copian de kie-cli** (`/Users/miguelacostabravo/Projects/mik3dev/kie-ai-cli/internal/`) y divergen libremente; el resto es nuevo porque la API de fal es distinta (tres hosts, códigos HTTP reales, cola con `request_id`). Cada paquete recibe cliente HTTP, reloj y sleep por parámetro para probarse con `httptest`. Ningún test usa la API real.

**Tech Stack:** Go 1.27, `github.com/spf13/cobra`, `golang.org/x/term`. Resto stdlib.

**Spec:** `docs/superpowers/specs/2026-10-17-fal-cli-design.md` (§3, §4, §7 y fila 1 de §8). Referencia de código: kie-cli, mismo autor, misma forma.

## Global Constraints

- Módulo `github.com/mik3dev/fal-cli`, binario `fal-cli`, `go 1.27`.
- Hosts: platform `https://api.fal.ai`, queue `https://queue.fal.run`, storage `https://rest.alpha.fal.ai`, schema `https://fal.ai`. Sobrescribibles con `FAL_PLATFORM_URL`, `FAL_QUEUE_URL`, `FAL_STORAGE_URL`, `FAL_SCHEMA_URL` (tests y smoke).
- Header `Authorization: Key <key>` en platform, queue y storage. La key nunca aparece en stdout, stderr, errores ni archivos fuera de `config.json`.
- Orden de la key: `FAL_API_KEY` → `FAL_KEY` → `config.json`. Vacía o solo espacios = no definida.
- Errores HTTP reales. Mapeo: 401/403 → `auth`; 422 → `validation` con `details.problems[]{field,message,type,ctx}`; 429 → `rate_limit`; cuerpo con `detail` que contenga `balance`, `credit` o `locked` (case-insensitive) → `credits`; resto → `api`. Un fallo de runner en status/result → `task_failed`.
- Códigos de salida: 0 ok · 1 `api`/`io` · 2 `usage`/`validation` · 3 `auth` · 4 `credits` · 5 `task_failed` · 6 `timeout` · 7 `rate_limit` · 8 `submit_uncertain`.
- stdout: **exactamente un** objeto JSON por comando. Progreso y avisos por stderr (`--quiet` los silencia).
- Estado en `~/.fal-cli/` (0700) o `FAL_CLI_HOME`. `config.json` 0600. Ledger `tasks.jsonl` con el endpoint de cada request.
- Caché: consultas de catálogo 1 h, schemas 24 h, precios 24 h.
- Limitador in-process para platform API: ≥500 ms entre llamadas. Reintento con backoff (1 s, 2 s, 4 s, máx. 3) en 429 y en fallos de red antes de enviar; GET siempre reintentable.
- Polling de status: 2 s → 5 s (×1.5). Timeout por defecto 30 m si la categoría contiene `video`, 10 m en otro caso.
- El resultado de la cola expira (~1 h; ~6 min si >10 KB): `run` descarga por defecto a `./fal-output/`.
- El id de endpoint lleva `/` y **no se codifica** en paths. En nombres de archivo `/` → `-`.
- Inmutabilidad: las funciones que transforman `input` devuelven mapas nuevos.
- Cobertura ≥80% por paquete.
- **La key real solo se usa en la Task 4 (captura, solo GET gratuitos) y nunca en CI ni en `go test` sin tag.**
- Commits: formato `<type>: <descripción>`; sin líneas de atribución.

## Review Focus

1. **`-p prompt=README.md` no debe subirse**: la subida solo aplica a claves que terminan en `_url` o `_urls`. → test en Task 9.
2. **Status `COMPLETED` con `error`/`error_type`** (fallo del runner): salida 5 `task_failed`, `details.requestId` y `details.errorType`; nunca intentar descargar. → test en Task 8.
3. **Ctrl-C durante `wait`**: el request queda en el ledger y stdout emite `error.code="timeout"` con `details.requestId` para retomar con `task wait`. → test en Task 8.
4. **Resultado expirado** (`GET …/requests/{id}` devuelve 404 o 410 tras `COMPLETED`): `task_failed` con mensaje que contiene `expired`; el agente no reintenta. → test en Task 8.
5. **`models` con `endpoint_id` que no existe** (platform 404 `not_found`): `validation` "unknown model", nunca `api`. → test en Task 5.

---

### Task 1: Esqueleto, contrato de salida y `version`

**Files:**
- Create: `go.mod`, `cmd/fal-cli/main.go`, `internal/output/output.go`, `internal/cli/root.go`, `internal/cli/version.go`, `Makefile`, `.gitignore`
- Test: `internal/output/output_test.go`, `internal/cli/version_test.go`
- Copiar de kie-cli: `internal/output/` y `internal/cli/{root,version}.go` cambiando el import y el nombre `kie-cli` → `fal-cli`.

**Interfaces:**
- Produces:
  - `output.Error{Code, Message string; Details map[string]any}` con `ExitCode() int`; constantes `CodeUsage, CodeValidation, CodeAuth, CodeCredits, CodeTaskFailed, CodeTimeout, CodeRateLimit, CodeSubmitUncertain, CodeAPI, CodeIO`.
  - `output.Success(w io.Writer, v map[string]any) error`, `output.Fail(w io.Writer, err error) int`.
  - `cli.Deps{Stdout, Stderr io.Writer; Stdin io.Reader; Env func(string) string; Now func() time.Time; Transport http.RoundTripper; Sleep func(context.Context, time.Duration) error}`.
  - `cli.NewRoot(deps Deps) *cobra.Command` (flags persistentes `--quiet`, `--debug`), `cli.Execute(deps Deps, args []string) int`, `var cli.Version = "dev"`.

- [ ] **Step 1: Write the failing tests** `TestExitCodes` (tabla de Global Constraints), `TestFailWritesSingleJSON` (exit 3, un solo objeto), `TestVersionCommand` (`{"ok":true,"version":"dev"}`), `TestUnknownFlagIsUsageError` (exit 2, `error.code=="usage"`).
- [ ] **Step 2: Run** `go test ./...`. Expected: FAIL.
- [ ] **Step 3: Implement.** `git init`; `go mod init github.com/mik3dev/fal-cli`; `go get github.com/spf13/cobra`. Copiar los archivos indicados. Makefile con `build`, `test` (`go test -race -cover ./...`), `install`, `lint`, `tidy-check`, `LDFLAGS -X github.com/mik3dev/fal-cli/internal/cli.Version=$(git describe --tags --always)`. `.gitignore` ya contiene `.env`: **conservar** y añadir `bin/`, `fal-output/`, `testdata/live/.raw/`.
- [ ] **Step 4: Run** `go test -race ./...`. Expected: PASS.
- [ ] **Step 5: Commit** `feat: scaffold fal-cli with JSON output contract and version`

---

### Task 2: Config, home y `auth login|status`

**Files:**
- Create: `internal/config/config.go`, `internal/cli/auth.go`
- Test: `internal/config/config_test.go`, `internal/cli/auth_test.go`
- Copiar de kie-cli `internal/config/` e `internal/cli/auth.go`.

**Interfaces:**
- Produces:
  - `config.Paths{Home, Config, Presets, Cache, Ledger string}`; `config.ResolvePaths(env) (Paths, error)` usa `FAL_CLI_HOME` o `<home>/.fal-cli`.
  - `config.Config{APIKey string "api_key"; OutDir string "out_dir"}`; `Load(p, warn)`, `Save(p, c)`, `EnsureHome(p)`.
  - `config.ResolveKey(env, c) (key, source string, err error)`: source `"env"` (de `FAL_API_KEY` o `FAL_KEY`) o `"config"`. Sin key: `{Code:"auth", Message:"no API key: set FAL_API_KEY or run 'fal-cli auth login'"}`.
  - `config.Redact(key) string`.
  - `fal-cli auth login` → `{"ok":true,"saved":"<path>"}`; `fal-cli auth status` → `{"ok":true,"source":"env|config","key":"…abcd"}`.

- [ ] **Step 1: Write the failing tests** (copiar los de kie-cli y renombrar las variables de entorno): `TestResolvePathsHonorsFAL_CLI_HOME`, `TestSaveWrites0600AndHome0700`, `TestLoadWarnsOnLoosePerms`, `TestResolveKeyOrder` (`FAL_API_KEY` gana a `FAL_KEY`, que gana a config), `TestResolveKeyEmptyEnvFallsBack`, `TestResolveKeyMissing`, `TestAuthLoginFromStdinPipe`, `TestRedactShortKey`.
- [ ] **Step 2: Run** `go test ./internal/config/ ./internal/cli/`. Expected: FAIL.
- [ ] **Step 3: Implement** copiando y adaptando. `go get golang.org/x/term`.
- [ ] **Step 4: Run**. Expected: PASS.
- [ ] **Step 5: Commit** `feat: config home ~/.fal-cli and auth login/status`

---

### Task 3: Cliente API de fal (tres hosts, errores HTTP, reintentos seguros)

**Files:**
- Create: `internal/api/client.go`, `internal/api/errors.go`, `internal/api/limiter.go`
- Test: `internal/api/client_test.go`, `internal/api/errors_test.go`
- Referencia: `kie-ai-cli/internal/api/` (misma estructura; cambia el decodificado y el mapeo).

**Interfaces:**
- Produces:
  - `type api.Host int` con `HostPlatform, HostQueue, HostStorage, HostSchema`.
  - `type api.Request struct{Method, Path string; Query url.Values; Body any; BodyFactory func() (contentType string, body io.ReadCloser, err error); Host Host; Retry bool; Auth bool}`. `Auth` por defecto true salvo `HostSchema`. `Retry` fuerza reintento de un POST (lo usa `task cancel`).
  - `api.New(key string, env func(string) string) *Client` con campos `PlatformURL, QueueURL, StorageURL, SchemaURL, Key string; HTTP, UploadHTTP *http.Client; Debug io.Writer; Sleep; Now`.
  - `func (c *Client) Do(ctx, req, out any) (*Response, error)` con `type Response struct{Status int; Header http.Header; Body []byte}`. `out` se llena con el body si es 2xx y `out != nil`.
  - `type api.Error struct{Status int; Type string; Message string; Problems []Problem; ErrorType string; Raw json.RawMessage}` con `type Problem struct{Field, Message, Type string; Ctx map[string]any}`.
    - Decodifica tres formas: platform `{"error":{"type","message"}}` → `Type`, `Message`; modelo `{"detail":[{loc,msg,type,ctx}]}` → `Problems` (campo = `loc` sin el prefijo `body`, unido con `.`, índices como `[n]`); request `{"detail":"…","error_type":"…"}` → `Message`, `ErrorType`. `detail` string sin `error_type` → `Message`.
  - `func (e *Error) AsOutput() *output.Error`: mapeo de Global Constraints. `Details` = `{"status":N,"type":…,"errorType":…,"problems":[…]}` (solo claves no vacías).
  - `type api.NetError struct{Sent bool; Err error}` igual que kie-cli.
  - Reintentos en `Do`: hasta 3 con `c.Sleep` (1 s, 2 s, 4 s) cuando `Method==GET`, `Retry`, `Status==429`, o `NetError.Sent==false`. POST sin `Retry` nunca se reintenta tras enviar.
  - Limitador: `HostPlatform` y `HostSchema` con espacio mínimo de 500 ms; los demás sin límite. `// ponytail: in-process only; add a file lock if parallel processes hit 429`.
  - `--debug` escribe `METHOD URL → status` con `Authorization: Key ****`.

- [ ] **Step 1: Write the failing tests** con `httptest.Server`:
  - `TestAuthHeaderIsKeyPrefix` (el server ve `Key k-secret`; `HostSchema` no lleva header).
  - `TestPlatformErrorDecodes` (`401 {"error":{"type":"authorization_error","message":"x"}}` → `AsOutput().Code=="auth"`, exit 3).
  - `TestForbiddenIsAuth` (`403 {"detail":"This API key is not permitted to perform this action."}` → `auth`).
  - `TestValidation422Problems` (`detail:[{"loc":["body","image_urls",0],"msg":"m","type":"missing"}]` → `Problems[0].Field=="image_urls[0]"`, `AsOutput().Code=="validation"`, `Details["problems"]` presente).
  - `TestRunnerErrorType` (`503 {"detail":"x","error_type":"runner_scheduling_failure"}` → `ErrorType`).
  - `TestBalanceMessageIsCredits` (`403 {"detail":"Insufficient balance"}` → `credits`, exit 4).
  - `TestGetRetries429ThenOK` (3 requests), `TestPostNoRetryOn500` (1 request), `TestPostRetryFlag` (`Retry:true`, 500 luego 200 → 2 requests).
  - `TestNoKeyLeak` (buffer `Debug` y `err.Error()` sin `k-secret`).
  - `TestPlatformLimiterSpacing` (dos llamadas platform → `Sleep` recibe ≥500 ms; dos a queue → 0 sleeps).
- [ ] **Step 2: Run** `go test ./internal/api/`. Expected: FAIL.
- [ ] **Step 3: Implement** según Interfaces.
- [ ] **Step 4: Run** `go test -race -cover ./internal/api/`. Expected: PASS, ≥80%.
- [ ] **Step 5: Commit** `feat: fal API client with typed errors, safe retries and platform limiter`

---

### Task 4: Captura real gratuita (solo GET) — PUNTO DE CONTROL

**Files:**
- Create: `scripts/capture/main.go`
- Create (output): `testdata/live/models/<consulta>.json` (primeras 2 páginas de `?limit=100` sin filtro y una página por cada `category` de interés), `testdata/live/schemas/<endpoint con "/"→"__">.json` (≥30 endpoints: 5 por categoría de imagen, video, audio, TTS, más los candidatos a preset de §5 del spec), `testdata/live/price/all.json` (pricing de esos endpoints), `testdata/live/billing.json` (respuesta cruda del 403), `testdata/live/REPORT.md`.

**Interfaces:**
- Consumes: `api.New`, `Client.Do` con `HostPlatform`/`HostSchema`.
- Produces: fixtures para Tasks 5, 6, 9, 10. Solo cuerpos de respuesta, nunca headers ni la key.

- [ ] **Step 1: Comprobar la key sin mostrarla**: `git check-ignore -q .env && grep -q '^FAL_API_KEY=.\+' .env && echo ok`. **Nunca** `cat`, `Read` ni imprimir `.env`. Sin key → detenerse y avisar.
- [ ] **Step 2: Implement** `scripts/capture/main.go`: solo `GET /v1/models…`, `GET /api/openapi/queue/openapi.json?endpoint_id=…`, `GET /v1/models/pricing…`, `GET /v1/account/billing?expand=credits`. Respeta el limitador (≈1 req/s para no gatillar 429). **Prohibido** POST a queue o storage. `REPORT.md`: total de endpoints por categoría, endpoints sin `metadata`, lista de nombres de propiedad de `*Input` que terminan en `_url`/`_urls` o tienen `ui.field`, lista de claves de `*Output` que son `$ref` a `File`/`Image`/`Video`/`Audio*` o arrays de ellos, y unidades de precio distintas.
- [ ] **Step 3: Run** `set -a; . ./.env; set +a; go run ./scripts/capture -out testdata/live`. Expected: ≈1–2 min.
- [ ] **Step 4: Verify** `set -a; . ./.env; set +a; grep -rF "$FAL_API_KEY" testdata/live | wc -l` → `0`.
- [ ] **Step 5: PUNTO DE CONTROL.** Mostrar `REPORT.md` al dueño. Detenerse si: algún schema no tiene `components.schemas.*Input`, si la forma de `File` difiere de `{url, content_type, file_name, file_size}`, o si los outputs usan claves de media que el extractor de Task 10 no cubre.
- [ ] **Step 6: Commit** `test: capture live catalog, schemas and pricing as fixtures (read-only, 0 USD)`

---

### Task 5: Caché, catálogo, precios y `credits`

**Files:**
- Create: `internal/cache/cache.go` (copiar de kie-cli), `internal/catalog/catalog.go`, `internal/cli/models.go`, `internal/cli/credits.go`
- Test: `internal/catalog/catalog_test.go`, `internal/cli/models_test.go`, `internal/cli/credits_test.go`

**Interfaces:**
- Produces:
  - `catalog.Model{EndpointID string "endpoint_id"; DisplayName "display_name"; Category; Status; Description string}` (recortado; `Description` truncada a 200 runas).
  - `catalog.Query{Type, Category, Text, Cursor string; Limit int; Deprecated bool}`. `Type` ∈ `image|video|audio|""` se traduce a las categorías de §4 del spec; si `Category` está presente, gana. Sin `Type` ni `Category` se excluyen `llm, vision, speech-to-text, image-to-3d` filtrando localmente.
  - `catalog.Page{Models []Model; NextCursor string; HasMore bool}`.
  - `catalog.Service{Client *api.Client; CacheDir string; Now func() time.Time}` con `List(ctx, q Query, refresh bool) (Page, error)`: una llamada por categoría del grupo cuando `Type` cubre varias; `// ponytail: Type with 3 categories = 3 requests merged in order, cursor only when a single category applies`. Si `Type` cubre varias categorías, `Cursor`/`NextCursor` se ignoran y `Limit` corta el total.
  - `Service.Find(ctx, endpoint string) (Model, bool, error)`: `?endpoint_id=`; 404 platform → `(Model{}, false, nil)`; un endpoint sin `metadata` devuelve `Model{EndpointID: id}` y `true`.
  - `Service.Price(ctx, endpoint string) (Price, error)` con `Price{UnitPrice float64 "unit_price"; Unit string; Currency string}`; endpoint ausente en `prices` → `{Code:"validation", Message:"unknown model: <id>"}`.
  - `catalog.Credits(ctx, c *api.Client) (balance float64, currency string, err error)`: 403 → `auth` con mensaje `"the API key lacks the BILLING permission; create one with API+BILLING at https://fal.ai/dashboard/keys"`.
  - Comandos: `models list [--type] [--category] [--deprecated] [--limit 50] [--cursor] [--refresh]` → `{"ok":true,"models":[…],"nextCursor":"…"|null,"hasMore":bool}`; `models search <texto> [...]` igual con `q`; `models price <endpoint>` → `{"ok":true,"model":…,"price":{unit_price,unit,currency}}`; `credits` → `{"ok":true,"balance":24.5,"currency":"USD"}`.
  - Helper `cli.newAPIClient(cmd, deps) (*api.Client, config.Paths, error)` (copiar de kie-cli).

- [ ] **Step 1: Write the failing tests** con un `httptest.Server` que sirve los fixtures de Task 4: `TestListVideoTypeMergesThreeCategories` (3 requests, sin cursor), `TestListSingleCategoryPassesCursor`, `TestListExcludesLLMByDefault`, `TestListCachesPerQuery` (segunda llamada: 0 requests; `refresh` fuerza), `TestFindUnknownIsFalse` (404 → `false, nil`), `TestFindWithoutMetadata`, `TestPriceUnknownIsValidation`, `TestCreditsForbiddenIsAuthWithHint`, `TestModelsListCommandJSON` (salida con `nextCursor`), `TestModelsSearchPassesQ`.
- [ ] **Step 2: Run** `go test ./internal/catalog/ ./internal/cli/`. Expected: FAIL.
- [ ] **Step 3: Implement** según Interfaces. Caché por nombre `catalog/<hash sha256 de la query>` TTL 1 h; `price/<endpoint>` TTL 24 h.
- [ ] **Step 4: Run** con `-race -cover`. Expected: PASS, ≥80%.
- [ ] **Step 5: Commit** `feat: models list/search/price with cache and credits`

---

### Task 6: Schema por endpoint, `$ref` y `models schema`

**Files:**
- Create: `internal/schema/schema.go`, `internal/schema/ref.go` (copiar de kie-cli), `internal/schema/summary.go` (copiar y adaptar), `internal/cli/schema.go`
- Test: `internal/schema/schema_test.go`, `internal/schema/summary_test.go`, `internal/cli/schema_test.go`

**Interfaces:**
- Produces:
  - `schema.Doc{Endpoint, Category string; Input, Output map[string]any; Raw json.RawMessage}`. `Input` es el schema del `requestBody` del `POST /{endpoint}` con `$ref` resueltos; `Output` el del `GET /{endpoint}/requests/{request_id}` 200. `Category` sale de `info.x-fal-metadata.category`.
  - `schema.Fetch(ctx, c *api.Client, cacheDir, endpoint string, refresh bool, now time.Time) (Doc, error)`: `GET {SchemaURL}/api/openapi/queue/openapi.json?endpoint_id=<id>` (`HostSchema`, sin auth); 404 → `{Code:"validation", Message:"unknown model: <id>"}`; caché `schemas/<id>` 24 h.
  - `schema.Resolve(doc map[string]any, node any) (any, error)` (de kie-cli; los `$ref` son `#/components/schemas/X`).
  - `schema.GuessLiterals(in map[string]any) map[string]any` (copiar de `kie-ai-cli/internal/schema/coerce.go`): strings que parecen número, booleano o JSON (`{…}`/`[…]`) se convierten; mapa nuevo. La coerción guiada por el schema llega en la etapa 2.
  - `schema.Summary{Required []string; Fields []Field; Output []Field}` con `Field{Name, Type, Description string; Required bool; Enum []any; Default any; Min, Max *float64; Items string}`; `schema.Summarize(d Doc) (Summary, error)`. `anyOf` de `[T, null]` se resume como `T` opcional; `anyOf` de `[$ref, enum]` lista `Type: "object|string"` y los `Enum`. Orden: `x-fal-order-properties` si existe.
  - `fal-cli models schema <endpoint> [--raw] [--refresh]` → `{"ok":true,"model":…,"category":…,"required":[…],"fields":[…],"output":[…]}`; `--raw` → `{"ok":true,"model":…,"openapi":<doc>}`.

- [ ] **Step 1: Write the failing tests** con fixtures de Task 4: `TestFetchExtractsInputAndOutput` (`fal-ai/flux/schnell`: `Required==["prompt"]`, `image_size` tiene enum y tipo `object|string`), `TestFetch404IsUnknownModel`, `TestFetchCaches24h`, `TestSummaryOrderFollowsXFalOrder`, `TestSummaryNullableAnyOf` (`seed` → `Type:"integer"`, no requerido), `TestSchemaCommandRaw`.
- [ ] **Step 2: Run** `go test ./internal/schema/ ./internal/cli/`. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** con `-race -cover`. Expected: PASS, ≥80%.
- [ ] **Step 5: Commit** `feat: models schema from the queue OpenAPI document`

---

### Task 7: Ledger local y `task list`

**Files:**
- Create: `internal/ledger/ledger.go` (copiar de kie-cli), `internal/cli/task.go` (solo `list` por ahora)
- Test: `internal/ledger/ledger_test.go`, `internal/cli/task_test.go`

**Interfaces:**
- Produces:
  - `ledger.Entry{RequestID string "requestId"; Endpoint string "endpoint"; State string; OutDir string "outDir"; CreatedAt time.Time "createdAt"; Note string}`.
  - `ledger.Append(path, e) error`, `ledger.List(path, limit, warn) ([]Entry, error)` (última línea por `RequestID`, más nuevas primero), `ledger.Find(path, requestID string) (Entry, bool)`.
  - `fal-cli task list [--limit 20]` → `{"ok":true,"tasks":[…]}`.

- [ ] **Step 1: Write the failing tests** (copiar de kie-cli renombrando campos): `TestAppendThenListNewestFirst`, `TestListKeepsLastStatePerRequest`, `TestListSkipsCorruptLinesWithWarning`, `TestFindMissing`, `TestTaskListCommand`.
- [ ] **Step 2: Run**. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run**. Expected: PASS.
- [ ] **Step 5: Commit** `feat: local request ledger and task list`

---

### Task 8: Cola — submit, status, result, cancel, wait

**Files:**
- Create: `internal/task/task.go`, `internal/task/wait.go` (copiar `poll` de kie-cli), `internal/cli/task.go` (añadir `get`, `wait`, `cancel`)
- Test: `internal/task/task_test.go`, `internal/task/wait_test.go`, `internal/cli/task_test.go`

**Interfaces:**
- Consumes: `api.Client`, `ledger`, `schema.Fetch` (para `--model` y timeout por categoría).
- Produces:
  - `task.Status{RequestID, State string; QueuePosition int; Error, ErrorType string; InferenceTime float64; Logs []string}`; `State` ∈ `IN_QUEUE|IN_PROGRESS|COMPLETED`.
  - `task.Submit(ctx, c, ledgerPath, endpoint string, input map[string]any, outDir string, now time.Time, warn io.Writer) (requestID string, err error)`: `POST {QueueURL}/<endpoint>` body = `input` tal cual. Éxito → ledger `State:"submitted"`. 422/401/403/429 → error mapeado, sin entrada en el ledger. `NetError.Sent==true` o 5xx tras enviar → `recoverUncertain`; si no resuelve → `{Code:"submit_uncertain", Message:"request may have been created; check https://fal.ai/dashboard/requests before resubmitting", Details:{"endpoint":…}}` y ledger `State:"unknown"` sin id.
  - `recoverUncertain(ctx, c, endpoint string, since time.Time) (requestID string, ok bool)`: `GET {PlatformURL}/v1/models/requests/by-endpoint?endpoint_id=<id>&start=<since RFC3339>&limit=5`; adopta el único `request_id` con `sent_at >= since`; con 0 o >1 candidatos devuelve `false`. Errores de esta consulta se ignoran (sigue siendo `submit_uncertain`).
  - `task.GetStatus(ctx, c, endpoint, id) (Status, error)`: `GET {QueueURL}/<endpoint>/requests/<id>/status?logs=1`. 404 → `{Code:"task_failed", Message:"request <id> not found or expired"}`.
  - `task.GetResult(ctx, c, endpoint, id) (json.RawMessage, error)`: `GET {QueueURL}/<endpoint>/requests/<id>`. 404/410 → `task_failed` con `"result expired or not found"`. 4xx/5xx con `detail` → `task_failed` con `details.errorType`/`problems`.
  - `task.Cancel(ctx, c, endpoint, id) (status string, err error)`: `PUT …/cancel` con `Retry:true`; 202 → `"CANCELLATION_REQUESTED"`, 400 → `"ALREADY_COMPLETED"`, 404 → `task_failed`.
  - `task.Waiter{Sleep, Now, Progress}`; `Wait(ctx, c, endpoint, id, timeout) (Status, error)`: polling 2 s → 5 s (×1.5); `COMPLETED` con `Error!=""` → `{Code:"task_failed", Message:"request <id> failed: <Error>", Details:{"requestId","errorType"}}`; timeout o ctx cancelado → `{Code:"timeout", Message:"timed out waiting for request <id>; resume with: fal-cli task wait <id>", Details:{"requestId"}}`. Progreso por stderr: `state: IN_QUEUE (position 3)`.
  - `cli.resolveEndpoint(paths, id, modelFlag string) (string, error)`: ledger, luego `--model`; sin ambos → `{Code:"usage", Message:"request <id> is not in the local ledger; pass --model <endpoint>"}`.
  - Comandos: `task get <id> [--model]` → `{"ok":true,"requestId","model","state","queuePosition"?,"result"?}` (`result` solo si `COMPLETED` y el GET funciona; no descarga). `task cancel <id> [--model]` → `{"ok":true,"requestId","model","status"}` y ledger `State:"cancelled"`. `task wait` se completa en Task 11.

- [ ] **Step 1: Write the failing tests** (`httptest` que simula la cola): `TestSubmitReturnsRequestIDAndLedgers`, `TestSubmit422IsValidationNoLedger`, `TestSubmit5xxAfterSendIsUncertainWhenHistoryEmpty` (ledger `unknown`, exit 8), `TestSubmit5xxRecoveredFromHistory` (history devuelve 1 request → éxito con ese id), `TestSubmitHistoryAmbiguousStaysUncertain` (2 candidatos), `TestWaitInQueueThenInProgressThenCompleted` (3 polls, sleeps 2 s y 3 s), `TestWaitCompletedWithErrorIsTaskFailed` (Review Focus 2), `TestWaitTimeoutKeepsRequestID`, `TestWaitCtxCancelIsTimeout` (Review Focus 3), `TestGetResultExpired` (Review Focus 4), `TestCancelAccepted`, `TestCancelAlreadyCompleted`, `TestTaskGetNeedsModelWhenNotInLedger` (exit 2).
- [ ] **Step 2: Run** `go test ./internal/task/ ./internal/cli/`. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** con `-race -cover`. Expected: PASS, ≥80%.
- [ ] **Step 5: Commit** `feat: queue submit/status/result/cancel with uncertain-submit recovery`

---

### Task 9: Subida automática de archivos locales

**Files:**
- Create: `internal/upload/upload.go`
- Test: `internal/upload/upload_test.go`

**Interfaces:**
- Consumes: `api.Client` (`HostStorage`, `UploadHTTP`).
- Produces:
  - `upload.IsFileKey(key string) bool`: `strings.HasSuffix(key, "_url") || strings.HasSuffix(key, "_urls")`. `// ponytail: name heuristic only; stage 2 adds ui.field and description from the schema`.
  - `upload.One(ctx, c *api.Client, path string) (fileURL string, err error)`: `POST {StorageURL}/storage/upload/initiate?storage_type=fal-cdn-v3` body `{"content_type": <mime por extensión, default application/octet-stream>, "file_name": <base>}` → `{file_url, upload_url}`; `PUT upload_url` con el archivo (`Content-Type` igual) usando `UploadHTTP` y `BodyFactory` (sin auth: `upload_url` es prefirmada). Archivo >90 MiB → `{Code:"io", Message:"<path> is larger than 90 MiB; multipart upload is not supported yet"}`. Archivo inexistente → `io`.
  - `upload.Resolve(ctx, c, input map[string]any, exists func(string) bool) (out map[string]any, uploaded []string, err error)`: mapa nuevo; para cada clave con `IsFileKey`, si el valor (string o elemento string de un `[]any`) no empieza por `http://`, `https://` ni `data:` y `exists(valor)`, lo sube y lo reemplaza; `uploaded` lista las rutas subidas en orden. Un valor que no existe se deja tal cual (la API responderá).

- [ ] **Step 1: Write the failing tests**: `TestOneInitiatesThenPuts` (el server recibe `content_type: image/png` y luego el PUT con el body exacto, sin header Authorization en el PUT), `TestOneTooLargeIsIO`, `TestResolveUploadsOnlyFileKeys` (Review Focus 1: `prompt=README.md` intacto, `image_url=./a.png` reemplazado), `TestResolveArrayItems` (`image_urls=[local, https://x]` → solo el primero sube), `TestResolveDataURIPassThrough`, `TestResolveDoesNotMutateInput`.
- [ ] **Step 2: Run** `go test ./internal/upload/`. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** con `-race -cover`. Expected: PASS, ≥80%.
- [ ] **Step 5: Commit** `feat: auto-upload local files in *_url fields to fal storage`

---

### Task 10: Extracción de resultados y descarga

**Files:**
- Create: `internal/result/result.go`, `internal/download/download.go` (copiar de kie-cli y quitar el fallback `download-url` y el parámetro `*api.Client`)
- Test: `internal/result/result_test.go`, `internal/download/download_test.go`

**Interfaces:**
- Produces:
  - `result.Media{Key, URL, ContentType, FileName string}`; `result.Extract(output json.RawMessage) []Media`: recorre el JSON en profundidad; recoge todo objeto con `url` string y (`content_type` string o `file_name` string); también strings `https://…` cuyo host termina en `fal.media` o `fal.run` bajo claves que terminan en `_url`/`_urls`. `Key` es la ruta de claves (`images[0]`, `video`, `audio`). Orden de aparición. Sin duplicados por URL.
  - `result.IDs(requestID string, output json.RawMessage, media []Media) map[string]any`: siempre `{"request_id": id}`; añade `"seed"` si el output lo trae en la raíz y, por cada `Media`, `<Key sin índices>_url` → URL (`images_url` para `images[0]` solo si hay una; si hay varias, `images_urls` lista).
  - `download.File{Path, URL, Kind string}`; `download.All(ctx, hc *http.Client, media []result.Media, outDir, endpoint, requestID string) ([]File, error)`: nombre `<endpoint con / → ->_<requestID>_<n>.<ext>`; `ext` del `ContentType` del `Media`, si no del header, si no de la URL; `Kind` ∈ `image|video|audio|file` por tipo MIME; nunca sobrescribe (sufijo `-1`, `-2`). Sin header Authorization. HTTPS obligatorio.

- [ ] **Step 1: Write the failing tests** con outputs de los fixtures de Task 4 (flux: `images[]`; kling: `video`; elevenlabs: `audio`): `TestExtractImagesArray`, `TestExtractSingleVideoFile`, `TestExtractAudioAndTimestampsIgnored`, `TestExtractDedupes`, `TestIDsHaveRequestAndSeed`, `TestDownloadNamesAndKinds` (`fal-ai-flux-schnell_<id>_0.jpeg`, `kind:"image"`), `TestDownloadNeverOverwrites`, `TestDownloadRejectsHTTP`.
- [ ] **Step 2: Run** `go test ./internal/result/ ./internal/download/`. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** con `-race -cover`. Expected: PASS, ≥80%.
- [ ] **Step 5: Commit** `feat: extract media from queue results and download with stable names`

---

### Task 11: `run` y `task wait` de punta a punta

**Files:**
- Create: `internal/cli/run.go`, `internal/cli/params.go` (copiar de kie-cli: `ParseParams`, `MergeInput`), modificar `internal/cli/task.go` (`wait`)
- Test: `internal/cli/run_test.go`, `internal/cli/task_test.go`

**Interfaces:**
- Consumes: todo lo anterior.
- Produces:
  - `fal-cli run <endpoint> -p k=v… [--input f|-] [--out DIR] [--dry-run] [--no-wait] [--timeout D] [--no-download] [--quiet]`. (`@preset` y `--no-validate` llegan en etapas 2 y 4; `--no-validate` se acepta y se ignora con aviso.)
  - Flujo: `schema.Fetch` (404 → `validation` unknown model) → `ParseParams` + `--input` + `schema.GuessLiterals` (copiar de kie-cli: números, booleanos, JSON) → `upload.Resolve` (salvo `--dry-run`) → `--dry-run` emite `{"ok":true,"dryRun":true,"model","input","pendingUploads":[…],"price":{…}}` (precio vía `catalog.Service.Price`; si falla, `price:null` y aviso) → `task.Submit` → `--no-wait` emite `{"ok":true,"requestId","model","state":"submitted","ids":{"request_id"},"uploaded":[…]}` → `task.Waiter.Wait` → `task.GetResult` → `result.Extract` → `download.All` (salvo `--no-download`) → `{"ok":true,"requestId","model","state":"success","files":[…],"ids":{…},"result":<raw>,"inferenceTime":n,"uploaded":[…]}`; ledger `State:"success"` (o `failed`/`timeout`).
  - Timeout por defecto según `Doc.Category` (`video` → 30 m, resto 10 m). `--out` por defecto `config.OutDir` o `./fal-output`.
  - Cualquier error tras el submit lleva `details.requestId`.
  - `fal-cli task wait <id> [--model] [--out] [--timeout] [--no-download]`: mismo tramo final que `run`; `--out` hereda del ledger si no se pasa; stderr avisa `result expires minutes after completion; download now`.

- [ ] **Step 1: Write the failing tests** (`httptest` que sirve schema, cola, storage y CDN): `TestRunHappyPathDownloads` (archivo creado, JSON con `files[0].path`, ledger `success`), `TestRunUnknownModel` (exit 2), `TestRunDryRunNoSubmitNoUpload` (0 POST), `TestRunNoWait`, `TestRunUploadsLocalImage` (`-p image_url=./x.png` → el submit recibe `https://…`), `TestRunRepeatedParamBuildsArray`, `TestRunTaskFailedCarriesRequestID`, `TestTaskWaitDownloadsUsingLedgerEndpoint`, `TestRunNoDownloadKeepsURLs`, `TestRunDefaultTimeoutVideo` (categoría `text-to-video` → 30 m en el `Waiter`).
- [ ] **Step 2: Run** `go test ./internal/cli/`. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `go test -race -cover ./...`. Expected: PASS, ≥80% por paquete. `make build` y `bin/fal-cli version` → JSON.
- [ ] **Step 5: Smoke manual gratuito** (sin gastar): `set -a; . ./.env; set +a; bin/fal-cli models search flux --limit 3`, `bin/fal-cli models schema fal-ai/flux/schnell`, `bin/fal-cli models price fal-ai/flux/schnell`, `bin/fal-cli run fal-ai/flux/schnell -p prompt="a red fox" --dry-run`, `bin/fal-cli credits` (espera `auth` con el hint BILLING). Expected: JSON válido en todos; 0 USD.
- [ ] **Step 6: Commit** `feat: run and task wait end to end with download`

---

## Fin de la etapa 1

Entregable: binario que hace el ciclo completo contra mocks y comandos gratuitos contra la API real. Antes de la etapa 2 el dueño revisa: salida de `models schema`, `REPORT.md` de la captura y la lista de unidades de precio. Pendientes conocidos para etapas siguientes: coerción y validación por schema (2), `ids` ricos y extractor calibrado (3), presets y skill (4), goreleaser/CI/README (5), smoke pagado con `FAL_E2E_BUDGET=2` (6).
