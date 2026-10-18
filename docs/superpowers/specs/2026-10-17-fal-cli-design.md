# fal-cli — Diseño

Fecha: 2026-10-17 · Estado: aprobado por el dueño (2026-10-17), presupuesto de pruebas 2 USD

## 1. Intención

CLI en Go (binario único `fal-cli`) para generar **imágenes, videos y audio** con la API de [fal.ai](https://fal.ai), diseñado como **herramienta para agentes de IA**. Es la hermana de `kie-cli` (`../kie-ai-cli`): misma forma de uso, mismo contrato de salida, mismos códigos de salida y presets con los mismos nombres.

**Motivo.** kie.ai es más barato, pero tiene mucha demanda y a veces es inestable. fal.ai es el **respaldo**. El agente ejecuta primero `kie-cli`; cuando kie falla o va lento, el agente decide saltar a `fal-cli`. Esa decisión es del agente: ni el CLI ni su skill implementan el fallback. Para que el salto sea barato, `fal-cli` debe sentirse idéntico a `kie-cli`.

**Decisión tomada en la investigación.** Existe `genmedia` (`fal-ai-community/genmedia-cli`, TypeScript/Bun, MIT, v0.7.0, 80 estrellas) con skills comunitarias. El dueño lo descartó: no confía en skills no oficiales con tan poca adopción, y la interfaz no coincide con kie-cli. Sirve como prior art (ver §3), no como base.

## 2. Objetivos, no-objetivos, anti-objetivos

**Objetivos (MVP)**
- Cubrir imagen, video y audio (música, TTS, efectos) en todos los modos que exponga el catálogo de fal: texto, imagen de referencia, frame inicial/final, video-a-video, edición, upscale, etc.
- Ser **dirigido por catálogo**: el agente consulta el catálogo vivo, lee el schema y nombra el endpoint exacto. Sin lista de modelos hardcodeada.
- Ciclo completo en un comando: subir archivos locales, enviar a la cola, esperar y **descargar**.
- Validación local contra el schema antes de enviar (evitar 422 que se cobran).
- Presets con los **mismos nombres** que kie-cli apuntando a modelos equivalentes de fal.
- Skill instalable `fal-generate` para Claude Code y otros agentes.
- Binario fácil de instalar (macOS, Linux, Windows), mismo mecanismo que kie-cli.

**No-objetivos**
- Modelos de chat/LLM, visión y speech-to-text (categorías `llm`, `vision`, `speech-to-text`): el agente ya es el LLM. Se pueden invocar con `run` porque están en el catálogo, pero no hay soporte específico ni presets.
- Webhooks. El CLI hace polling.
- Camino síncrono `fal.run`: todo va por `queue.fal.run`.
- TUI, modo interactivo, galería, auto-update, telemetría.
- Lógica de fallback kie→fal (es del agente).
- Entrenamiento (`kind: training`), 3D (`image-to-3d`): fuera del MVP; `run` los acepta si el schema lo permite, sin garantías.

**Anti-objetivos**
- Que el agente adivine ids de endpoint o nombres de campo.
- Gastar dinero en una petición que se podía rechazar localmente.
- Perder assets porque el resultado de la cola expiró antes de descargarlo.
- Salida no parseable: texto en stdout, prompts interactivos, errores ambiguos.
- Que la API key se filtre a logs, stdout o al repo.
- Que el agente tenga que aprender una interfaz distinta a la de kie-cli.

## 3. Hechos de la API de fal que condicionan el diseño

Verificados el 2026-10-17 contra la API real (catálogo, pricing, schemas) y la documentación oficial.

| Hecho | Consecuencia |
|---|---|
| Catálogo vivo: `GET https://api.fal.ai/v1/models` con `q`, `category`, `status` (`active`/`deprecated`), `endpoint_id` (repetible, modo "find"), `sort`, `cursor`/`limit`, `expand=openapi-3.0`. Responde `{models[], next_cursor, has_more}`. 12 categorías: `text-to-image`, `image-to-image`, `text-to-video`, `image-to-video`, `video-to-video`, `text-to-audio`, `text-to-speech`, `audio-to-audio`, `image-to-3d`, `llm`, `vision`, `speech-to-text`. ~1200 endpoints. | `models list/search` consultan el catálogo con caché local. `--type image|video|audio` se traduce a grupos de categorías (ver §4); `--category` pasa la categoría nativa. |
| Funciona sin key, pero devuelve 429 muy pronto. Con key, límite más alto. `metadata` puede faltar en algunos endpoints (p. ej. `fal-ai/veo3.1/fast` aparece en pricing pero sin metadata en el catálogo). El modo "find" devuelve 404 si **alguno** de los ids no existe. | Enviar siempre la key. Tolerar `metadata` ausente. Resolver ids de uno en uno cuando se valida un modelo. |
| Schema: OpenAPI 3.0.4 por endpoint en `GET https://fal.ai/api/openapi/queue/openapi.json?endpoint_id=…` (sin key; 404 si el endpoint no existe; **sí** responde para endpoints sin `metadata` en el catálogo, como `fal-ai/veo3.1/fast`). El catálogo también lo incluye con `expand=openapi-3.0`. `paths` tiene `POST /{endpoint}` (requestBody → `components/schemas/<X>Input`) y `GET /{endpoint}/requests/{id}` (→ `<X>Output`). `$ref` locales, `anyOf` con `null`, `x-fal-order-properties`, `default`, `examples`, `enum`, `ui.field: image` en algunos campos. | **Fuente canónica del schema: el endpoint `openapi.json`** (decisión tras la revisión del builder); el catálogo solo aporta metadata (categoría, estado, descripción) y puede faltar. Si el schema da 404, el endpoint no existe → error `validation` "unknown model" (igual que kie-cli). No hay ruta "sin schema": el schema es obligatorio para `run`. Un resumen de campos derivado del schema. Validación JSON Schema con `$ref` resueltos. Mucho más limpio que kie (no hay JSON-en-string ni schemas `null`). |
| Campos de archivo de entrada: `image_url`, `image_urls[]`, `tail_image_url`, `reference_audio_url`, `video_url`, `audio_url`… Son `string` sin `format: uri`; a veces traen `ui.field`. | Heurística de "campo de archivo": nombre termina en `_url`/`_urls`, o `ui.field` ∈ {image, video, audio, file}, o la descripción menciona URL. El builder la calibra en la etapa de verificación real contra todos los schemas. |
| Cola: `POST https://queue.fal.run/{endpoint}` → `{request_id, status_url, response_url, cancel_url, queue_position}`. `GET …/requests/{id}/status` → `IN_QUEUE` (con `queue_position`) / `IN_PROGRESS` / `COMPLETED` (con `metrics.inference_time`, y `error`+`error_type` si falló). `GET …/requests/{id}` → output del modelo. `PUT …/requests/{id}/cancel` → 202 `CANCELLATION_REQUESTED`, 400 `ALREADY_COMPLETED`, 404. Header `Authorization: Key <FAL_KEY>`. | Un motor genérico de cola para todo. Polling de status, luego GET del resultado. `task cancel` es barato. |
| **El resultado de la cola expira**: ~1 h tras completar, y ~6 min si el resultado pesa más de 10 KB. | `run` espera y descarga por defecto. `--no-wait` existe, pero `task wait` debe correr pronto; la skill lo advierte. Un `task get/wait` sobre un resultado expirado devuelve `task_failed` con mensaje "result expired". |
| Salida de media: objeto `File {url, content_type, file_name, file_size}` (a veces con `width`/`height`) bajo claves `images[]`, `image`, `video`, `audio`, `audio_file`, etc. Host CDN `v3b.fal.media` / `fal.media`. Retención del archivo ≥7 días por defecto (configurable con `X-Fal-Object-Lifecycle-Preference`). | Extractor de resultados: recorrer el output y recoger todo objeto con `url` + `content_type`, o strings `https://…fal.media/…`. `files[].kind` sale del `content_type`. |
| Subida: `POST https://rest.alpha.fal.ai/storage/upload/initiate?storage_type=fal-cdn-v3` con `{content_type, file_name}` → `{file_url, upload_url}`; luego `PUT upload_url` con el cuerpo. Multipart (`initiate-multipart`, partes de 10 MB, `complete`) solo para >90 MB. | Subida automática de rutas locales. Multipart queda **fuera del MVP**: archivos >90 MB devuelven error `io` claro. |
| Errores con códigos HTTP reales. 422 → `{detail:[{loc, msg, type, ctx?, input?}]}` con `type` tipado (`missing`, `one_of`, `image_too_large`, `content_policy_violation`, `file_download_error`…). Errores de runner 5xx/504 → `{detail: "…", error_type: "runner_…"}` con header `X-Fal-Error-Type`. Header `X-Fal-Needs-Retry`. 401/403 → `{detail: "…"}` o `{error:{type:"authorization_error", message}}`. Platform API (catálogo, pricing) → `{error:{type, message, request_id}}` con `type` ∈ `authorization_error`, `validation_error`, `not_found`, `rate_limited`, `server_error`. Un 422 puede cobrarse si el runner gastó GPU. | Mapeo de errores a los códigos de salida de kie-cli (§4). Validación local primero. |
| Precio: `GET https://api.fal.ai/v1/models/pricing?endpoint_id=…` (con key, hasta 50 ids) → `{prices:[{endpoint_id, unit_price, unit, currency}]}`. Unidades variadas: `images`, `megapixels`, `seconds`, `1000 characters`, `minutes`, `audios`, `generations`, `1m tokens`, `units`. Hay `POST /v1/models/pricing/estimate`. | `models price` muestra `unit_price`+`unit`. `--dry-run` muestra el precio unitario; **no** calcula el total (las unidades dependen del output). |
| Saldo: `GET https://api.fal.ai/v1/account/billing?expand=credits` → `{credits:{current_balance, currency}}`, pero requiere key con permiso `BILLING`. La key actual del dueño (preset `API`) devuelve 403 "not permitted". Sin saldo, fal rechaza peticiones al bajar del umbral de bloqueo de la cuenta. | `credits` es mejor esfuerzo: con 403 devuelve error `auth` y mensaje "la key necesita permiso BILLING". La skill dice que no es bloqueante. |
| Rate limits de ejecución: por **concurrencia** por cuenta (2 simultáneas al inicio, hasta 40 según compras). El exceso se encola, no falla. Platform API devuelve 429 (`rate_limited`) sin cifras documentadas. | Limitador suave en el cliente para platform API (≈2 req/s) y reintento con backoff en 429. Polling de status cada 2–5 s. |
| Historial: `GET /v1/models/requests/by-endpoint?endpoint_id=…&start=…` (permiso `models:requests:read`, incluido en el preset `API`) lista requests recientes con `request_id`, `status_code`, `sent_at`. Exige `endpoint_id`. | Ledger local igual que kie (`tasks.jsonl`, con `endpoint`). Resolución de `submit_uncertain`: antes de rendirse, el CLI consulta este historial para el endpoint y los últimos minutos; si encuentra un request nuevo lo adopta. |
| El id de modelo lleva `/` (`fal-ai/flux/schnell`, `openai/gpt-image-2`, `elevenlabs/music/v2.5`). | Nunca codificar el slash en paths. Nombre de archivo: `/` → `-`. |

### Prior art
- **genmedia** (`fal-ai-community/genmedia-cli`): `models`, `schema`, `run --<param>`, `--async`, `status`, `upload`, `pricing`, `--download`, `--json`. Sin subida automática de rutas locales, sin validación local ni dry-run, sin presets, sin ledger, exit code siempre 1, telemetría PostHog y auto-update. Ideas que se toman: `--download` con plantilla de nombre no; sí la idea de `status --cancel` (→ `task cancel`) y el resumen compacto del schema.
- **SDKs oficiales** `@fal-ai/client` (JS) y `fal-client` (Python): referencia para subida de archivos y polling. No hay SDK Go oficial.
- **kie-cli**: base de arquitectura, contrato y skill.

## 4. Interfaz

Idéntica a kie-cli salvo lo marcado.

```
fal-cli auth login | status                       # guarda key (o usa FAL_API_KEY / FAL_KEY)
fal-cli credits                                   # saldo (requiere permiso BILLING en la key)
fal-cli models list   [--type image|video|audio] [--category text-to-video] [--deprecated] [--limit 50] [--cursor C] [--refresh]
fal-cli models search "<texto>" [--type ...] [--category ...] [--limit 50] [--cursor C]
fal-cli models schema <endpoint> [--raw]          # resumen de campos de input y output; --raw = OpenAPI
fal-cli models price  <endpoint>                  # unit_price + unit
                                                  # (sin `models health`: fal no expone success-rate)

fal-cli run <endpoint|@preset> -p clave=valor ... [--input file.json] [--out DIR]
        [--dry-run] [--no-wait] [--timeout 30m] [--no-download] [--no-validate] [--quiet]
fal-cli task list [--limit 20]                    # ledger local
fal-cli task get    <requestId>                   # estado + resultado si está listo (no descarga)
fal-cli task wait   <requestId> [--out DIR] [--timeout ...] [--no-download]
fal-cli task cancel <requestId>                   # NUEVO respecto a kie-cli

fal-cli preset list | show <name>
fal-cli skills install [--target DIR]
fal-cli version
```

**`--type`** agrupa categorías: `image` = `text-to-image`, `image-to-image`; `video` = `text-to-video`, `image-to-video`, `video-to-video`; `audio` = `text-to-audio`, `text-to-speech`, `audio-to-audio`. `--category` acepta cualquier categoría nativa. Por defecto `models list/search` excluyen `deprecated` (`--deprecated` los incluye) y las categorías `llm`, `vision`, `speech-to-text`, `image-to-3d` salvo que se pidan con `--category`.

**Paginación (mi decisión, distinta de kie-cli por el tamaño del catálogo, ~1200 endpoints)**: `models list/search` devuelven como máximo `--limit` entradas (por defecto 50) y la salida lleva `{"models":[…],"nextCursor":"…"|null,"hasMore":bool}`; `--cursor` continúa. Cada entrada se recorta a `endpoint_id`, `display_name`, `category`, `status`, `description`. La caché guarda por consulta (filtros + cursor), TTL 1 h.

**`task get/wait/cancel` reciben solo el `requestId`**: el endpoint se toma del ledger local. Si el id no está en el ledger, se acepta `--model <endpoint>` para indicarlo.

### Reglas de `run`
- **Claves exactas del schema** en `-p`. Clave repetida sobre un campo array acumula valores. Valor que empieza con `@` se lee de archivo (`@@` = `@` literal). `--input` acepta JSON con el objeto completo; `-p` lo sobrescribe. Precedencia: preset < `--input` < `-p`.
- **Conversión de tipos** según el schema (string, integer, number, boolean, array, object, `anyOf` con `null`). Objetos y arrays de objetos vía `--input` o `-p clave=<json>`.
- **Subida automática**: si el valor de un campo de archivo (heurística de §3) es una ruta local existente, se sube al storage de fal y se reemplaza por `file_url`. Se aplica a elementos de arrays. URLs `http(s)://` y `data:` pasan tal cual. Archivos >90 MB: error `io`.
- **Validación previa** contra el schema de input con `$ref` resueltos: requeridos, tipos, enums, `anyOf`, min/max, `minItems`/`maxItems`. Errores con campo, valor y lo esperado. `--no-validate` degrada a avisos por stderr (la coerción se aplica igual).
- **`--dry-run`**: valida, no sube nada, imprime el body final (rutas locales marcadas como pendientes de subir), el endpoint y `{unit_price, unit}`. No envía nada.
- **Por defecto**: enviar → polling de status (2 s a 5 s, tope `--timeout`: 10 m imagen/audio, 30 m video según categoría) → GET resultado → descargar a `--out` (por defecto config `out_dir`, si no `./fal-output/`).
- **`--no-wait`**: devuelve `{requestId, model, state: "submitted"}` al instante y registra en el ledger. `task wait` retoma con la misma descarga. Stderr avisa que el resultado expira en minutos.
- **Reintentos**: `POST` de submit se reintenta solo cuando es seguro que no se creó request (fallo de conexión antes de enviar, 429, 503 con `X-Fal-Needs-Retry`). Timeout de lectura o 5xx ambiguo tras enviar → consulta el historial por endpoint (§3); si no lo resuelve, error `submit_uncertain` (exit 8) y ledger con `state: unknown`. Status, resultado y descargas se reintentan con backoff.
- **Resultado fallido**: status `COMPLETED` con `error`, o GET del resultado con 4xx/5xx → `task_failed` (exit 5) con `details.requestId`, `details.errorType` y el `detail` original.

### Contrato de salida
- **stdout**: un objeto JSON por comando. **stderr**: progreso y avisos (`--quiet` los silencia).
- `run` exitoso:
```json
{"ok":true,"requestId":"…","model":"fal-ai/flux/schnell","state":"success",
 "files":[{"path":"fal-output/fal-ai-flux-schnell_<requestId>_0.jpeg","url":"https://v3b.fal.media/…","kind":"image"}],
 "ids":{"request_id":"…"},"result":{…output crudo…},
 "price":{"unit_price":0.003,"unit":"megapixels","currency":"USD"},"inferenceTime":1.2,"uploaded":[…]}
```
  Con preset: clave `"preset"`. `ids` lleva `request_id` siempre y, cuando el output los tenga, `seed` y las URLs de media por clave (`image_urls`, `video_url`…) para encadenar; el builder decide el detalle al ver outputs reales.
- Error: `{"ok":false,"error":{"code","message","details"?}}`.
- **Códigos de salida (iguales a kie-cli)**: 0 ok · 2 `usage`/`validation` · 3 `auth` · 4 `credits` · 5 `task_failed` · 6 `timeout` · 7 `rate_limit` · 8 `submit_uncertain` · 1 `api`/`io`.
- **Mapeo fal → código**: 401/403 (o `authorization_error`) → `auth`; mensaje de saldo/bloqueo de cuenta (`detail` que mencione balance/credits/locked; el builder lo confirma en la etapa real) → `credits`; 422 local o del servidor → `validation` con `details.problems[] {field, message, type, ctx}`; 429 / `rate_limited` → `rate_limit`; 5xx de runner en el resultado → `task_failed`; resto → `api`.
- Nombre de archivo: `<endpoint con / → ->_<requestId>_<n>.<ext>`. Extensión del `content_type`, luego de la URL. Nunca sobrescribe: sufijo.

### Configuración y secretos
Todo en **`~/.fal-cli/`** (0700), sobrescribible con `FAL_CLI_HOME`.

```
~/.fal-cli/
  config.json     # 0600 — api_key, out_dir, timeouts
  presets.yaml    # presets del usuario
  cache/          # catálogo (TTL 1h) y schemas (TTL 24h); --refresh invalida
  tasks.jsonl     # ledger: requestId, endpoint, fecha, out, state
```

- Orden de la key: `FAL_API_KEY` (convención de kie-cli y del `.env` del dueño) → `FAL_KEY` (nombre oficial de los SDK) → `config.json`. `auth login` lee de stdin sin eco. `auth status` dice de dónde viene la key, nunca su valor.
- Aviso si `config.json` tiene permisos más abiertos que 0600. La key nunca se imprime; en `--debug` va redactada.

## 5. Presets

YAML, embebidos con `go:embed`; los del usuario en `~/.fal-cli/presets.yaml` sobrescriben por nombre. Formato igual a kie-cli: `name`, `description` (cuándo usarlo y qué campos faltan), `model`, `params`.

**Mismos nombres que kie-cli.** Candidatos verificados en el catálogo el 2026-10-17 (precio en USD según `pricing`); el builder fija la lista en la etapa 5 y el dueño la revisa después:

| Preset | Candidato fal | Precio |
|---|---|---|
| `imagen-rapida` | `fal-ai/flux/schnell` | 0.003 / megapíxel |
| `imagen-calidad` | `fal-ai/nano-banana-pro` (alt. `fal-ai/nano-banana-2`, 0.08) | 0.15 / imagen |
| `imagen-edicion` | `fal-ai/nano-banana-2/edit` (alt. `nano-banana-pro/edit`, 0.15) | 0.08 / imagen |
| `video-calidad` | `fal-ai/kling-video/v2.5-turbo/pro/text-to-video` | 0.07 / s (≈0.35 por 5 s) |
| `video-imagen` | `fal-ai/kling-video/v2.5-turbo/pro/image-to-video` | 0.07 / s |
| `video-inicio-fin` | `fal-ai/kling-video/v2.5-turbo/pro/image-to-video` con `image_url` + `tail_image_url` (o `fal-ai/veo3.1/fast/first-last-frame-to-video`, 0.15 / s) | 0.07 / s |
| `video-barato` | `minimax/h3-max-turbo/text-to-video` (0.016 / s) o `fal-ai/minimax/hailuo-02/standard/text-to-video` (0.045 / s) | ≈0.08–0.23 por 5 s |
| `musica` | `fal-ai/minimax-music/v2.6` (0.15 / audio) o `minimax/music-3` (0.002 / s) | — |
| `instrumental` | mismo modelo que `musica` con el parámetro que exponga el schema | — |
| `voz` | `fal-ai/elevenlabs/tts/multilingual-v2` (0.10 / 1000 car.) o `fal-ai/elevenlabs/tts/turbo-v2.5` (0.05) | — |

Los presets se guardan con los nombres de campo del schema de fal (p. ej. `duration: "5"` solo si el schema lo pide como string).

## 6. Skill `fal-generate`

- En el repo, `skills/fal-generate/`: `SKILL.md` + `references/{image,video,audio}.md`. Misma estructura y tono que `kie-generate`. Embebida en el binario; `fal-cli skills install` la copia a `~/.claude/skills/` o a `--target`.
- Contenido: flujo (search/list → `models schema` → `models price` → `--dry-run` si es caro → `run` → usar `files[].path`), cómo pasar input, presets, encadenado con `ids`, tabla de errores y códigos de salida.
- Avisos propios de fal: el resultado de la cola expira en minutos (usar `--no-wait` solo si se hará `task wait` enseguida); `credits` puede fallar por permisos de la key y no es bloqueante; el precio es unitario y el total depende del output; un 422 del servidor puede haberse cobrado.
- **No contiene** lógica de fallback kie→fal: eso lo decide el agente.
- Un test verifica que cada comando y flag mencionado en la skill exista en el CLI.

## 7. Arquitectura (decisión del builder, sugerencia)

```
cmd/fal-cli/main.go
internal/
  api/       cliente HTTP (platform api.fal.ai, queue.fal.run, rest.alpha.fal.ai), mapeo de errores, limitador
  catalog/   list/search/find/price + caché + grupos --type
  schema/    extracción Input/Output del OpenAPI, resolución $ref, resumen, coerción, validación
  upload/    initiate + PUT, detección de rutas locales, heurística de campos de archivo
  task/      submit, polling de status, GET resultado, cancel, resolución de submit_uncertain
  result/    extracción de files (File{url,content_type}) e ids
  download/  descarga y nombrado          (copiado de kie-cli)
  preset/    carga y merge                (copiado de kie-cli)
  ledger/    tasks.jsonl                  (copiado de kie-cli, campo endpoint)
  config/    home, config.json, key       (copiado de kie-cli, nombres FAL_*)
  skills/    embed + install              (copiado de kie-cli)
  output/    JSON stdout, códigos         (copiado de kie-cli)
  cli/       comandos cobra
skills/fal-generate/...
presets/default.yaml
```

Módulo Go `github.com/mik3dev/fal-cli`, repo privado. Dependencias: `spf13/cobra`, `gopkg.in/yaml.v3`, `santhosh-tekuri/jsonschema/v6`, `golang.org/x/term`. Resto stdlib. Los paquetes marcados "copiado" se copian de kie-cli (son `internal` de otro módulo; importar no es posible y no se quiere acoplar). Distribución igual que kie-cli: `make install`, `go install` con `GOPRIVATE`, goreleaser + GitHub Releases privado vía `gh release download`.

## 8. Etapas

Una etapa a la vez; el dueño revisa al final de cada una. Pruebas contra mocks construidos con respuestas reales capturadas sin costo (catálogo, schemas, pricing son GET gratuitos). **Uso de la API real**: solo en smoke tests manuales, nunca en CI.

| # | Etapa | Contenido | Estim. |
|---|---|---|---|
| 1 | Núcleo | **Paso 0: captura real gratuita** con la key: catálogo completo, schemas de una muestra amplia (todas las categorías), pricing de varios ids; guardados en `testdata/live/` sin la key. Luego: auth/config, cliente API + errores, `models list/search/schema/price` + caché, `credits`, upload, `run` (submit + polling + resultado + descarga), `task list/get/wait/cancel`, ledger, contrato JSON y códigos de salida. Copia de los paquetes reutilizables de kie-cli. | 2–3 días |
| 2 | Validación | resolución `$ref`, coerción, validación con `anyOf`, mensajes útiles, `--dry-run` con precio, heurística de campos de archivo calibrada contra los schemas capturados | 1–2 |
| 3 | Resultados e ids | extractor sobre outputs reales de todas las categorías de interés, `ids` para encadenar, `submit_uncertain` con historial | 0.5–1 |
| 4 | Presets + skill | presets con nombres de kie, skill `fal-generate` + references, `skills install`, test de consistencia skill↔CLI | 1 |
| 5 | Distribución | goreleaser, CI (test + lint), README | 0.5 |
| 6 | Verificación real | con la key: recorrer el catálogo y todos los schemas (resumen, campos de archivo, validación); smoke tests pagados dentro del presupuesto (§9); corregir y refrescar fixtures | 1 |
| | **Total** | | **≈ 6–8.5 días** |

Menos que kie-cli: la API es más limpia y se reutiliza código. Si en la etapa 6 el catálogo o los schemas no se comportan como aquí se describe, el builder se detiene y consulta antes de cambiar el diseño.

## 9. Pruebas
- Unitarias: coerción, `$ref`, validación, extracción de resultados (fixtures reales), nombrado, mapeo de errores, heurística de archivos.
- Integración: `httptest` que imita platform API, cola (submit/status/result/cancel), storage upload y errores (422 `detail[]`, 429, 5xx de runner, resultado expirado).
- Smoke reales: build tag `live` + `FAL_E2E=1`, solo a mano. Key desde `.env` (ya existe en la raíz del repo, gitignored) como `FAL_API_KEY`; la key tiene el preset `API` (sin `BILLING`). **Presupuesto (decisión del dueño, 2026-10-17)**: `FAL_E2E_BUDGET` en USD, por defecto **2**. Antes de cada generación pagada se comprueba `models price` y se salta si no cabe. Se generan de verdad: 2 imágenes baratas (≈0.01), 1 TTS corto (≈0.01), 1 clip de música barato (≈0.03–0.15), y 1 video barato de 5 s (≈0.1–0.25) si el presupuesto lo permite; el resto de video solo `--dry-run`.

## 10. Riesgos y preguntas abiertas
- La key actual no tiene permiso `BILLING`: `credits` fallará hasta que el dueño cree una key con `API`+`BILLING` (opcional).
- Forma exacta del error "sin saldo": no documentada; se confirma en la etapa 6.
- Heurística de campos de archivo: calibrar contra todos los schemas en la etapa 6.
- Unidades de precio heterogéneas: `--dry-run` no puede dar un total fiable.
- Expiración del resultado en 6 min para outputs grandes: `--no-wait` es menos útil que en kie; la skill lo deja claro.
- Catálogo de 1200 endpoints: resuelto con paginación explícita en `models list/search` (§4).
