# fal-cli — Etapa 2: Validación y coerción — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `run` convierte los `-p k=v` al tipo que declara el schema del endpoint, valida el input antes de gastar dinero y detecta los campos de archivo con el schema (no solo por nombre).

**Architecture:** Dos funciones nuevas en `internal/schema` (`Coerce`, `Validate`, portadas de kie-cli sobre el schema de input ya resuelto por `schema.Fetch`), una heurística de campo de archivo en `internal/upload` que lee el schema, y el cableado en `internal/cli/run.go`: parse → coerce → validate → upload → submit. `--dry-run` muestra el body coercionado y los problemas; `--no-validate` degrada a avisos.

**Tech Stack:** Go 1.27, `github.com/santhosh-tekuri/jsonschema/v6` v6.0.2 (draft 2020, igual que kie-cli), `golang.org/x/text` (mensajes del validador). Sin otras dependencias.

**Spec:** `docs/superpowers/specs/2026-10-17-fal-cli-design.md` (§4 "Reglas de `run`", §3 fila "Campos de archivo", §8 etapa 2, §9).

## Global Constraints

- Misma interfaz y contrato JSON que kie-cli: `validation` → exit 2 con `details.problems[] {field, message, got?}`.
- Precedencia de input: `--input` < `-p` (preset llega en la etapa 4). `-p` sobre clave array acumula.
- La coerción nunca falla: un valor inconvertible se deja tal cual y lo reporta la validación.
- `--no-validate`: la coerción se aplica igual; los problemas van a stderr como `warning: validation: <field>: <message>` y el envío sigue.
- Nunca mutar los mapas de entrada: `Coerce`, `Validate` y la heurística devuelven copias.
- Los schemas capturados usan `anyOf [T, null]`, `exclusiveMinimum` numérico, sin `nullable` ni `oneOf` (verificado 2026-10-18 sobre los 47 fixtures): el compilador draft 2020 los acepta tal cual; `ui`, `x-fal-order-properties` y `examples` son palabras desconocidas y se ignoran.
- La key nunca llega a stdout/stderr/mensajes (sin cambios de superficie de red en esta etapa).
- Cobertura ≥80% por paquete; `go test -race ./...` y `go vet ./...` verdes antes de cada commit.

## Review Focus

1. **`-p seed=3` con `seed: anyOf [integer, null]`** → el body lleva `3` numérico, no `"3"`; sin problema de validación. → test en Task 1 y Task 4.
2. **`-p image_size=landscape_4_3` y `-p image_size={"width":512,"height":512}`** (anyOf [objeto, enum string]) → string queda string, JSON pasa a objeto; `-p image_size=bogus` → `validation` con los valores permitidos en el mensaje. → tests en Task 1, 2 y 4.
3. **Clave inexistente `-p promt=x`** con schema cerrado (`additionalProperties: false`) → `validation` "unknown field" con exit 2 **antes** de subir o enviar. → tests en Task 2 y 4.
4. **`-p num_images=99`** (máximo 4) → `validation` con `field: num_images`, `got: 99` y mensaje con el máximo; con `--no-validate` se envía con aviso. → tests en Task 2 y 4.
5. **Campo de archivo sin sufijo `_url`** (`ui.field: image` en el schema) con ruta local → se sube; `-p prompt=README.md` nunca se sube aunque exista el archivo. → test en Task 3.

---

### Task 1: `schema.Coerce` (tipado por schema)

**Files:**
- Create: `internal/schema/coerce.go` (ampliar: hoy solo tiene `GuessLiterals`)
- Test: `internal/schema/coerce_test.go`

**Interfaces:**
- Consumes: `schema.Doc.Input` (schema de input con `$ref` resueltos; propiedades en `properties`, nulabilidad vía `anyOf [T, {type: null}]`).
- Produces:
  - `schema.PropSchema(sch map[string]any, key string) (map[string]any, bool)`: busca `key` en `properties`, luego en las ramas `oneOf/anyOf/allOf` en orden. Primera coincidencia gana.
  - `schema.Coerce(sch, input map[string]any) map[string]any`: mapa nuevo; cada valor cuyo tipo declarado (propio `type` o unión de tipos de `anyOf`/`oneOf`, ignorando `null`) no coincida se convierte en el orden `integer, number, boolean, array, object, string`. Strings → `json.Number` para integer/number (trim de espacios), `true/false` (case-insensitive) para boolean, JSON para array/object (`[...]`/`{...}`), un string sobre un campo array se envuelve `[s]`. Arrays coercionan sus `items`; objetos sus `properties` conocidas (una sola profundidad bastante: usar recursión). Claves no declaradas o valor `nil` se dejan igual. **Puerto directo de kie-cli `internal/schema/coerce.go`** (`declaredTypes`, `coerceValue`, `matches`, `fromString`, `stringToArray`, `coerceItems`, `coerceNested`, `scalarString`).

- [ ] **Step 1: Write the failing tests** en `coerce_test.go`, usando el schema real: `d := fetch(t, "fal-ai/flux/schnell")` (helper ya existe en `schema_test.go`) y `sch := d.Input`.

```go
func TestCoerceTypedByFluxSchema(t *testing.T)   // "num_inference_steps":"4"→json.Number("4"); "seed":"3"→json.Number("3") (anyOf[integer,null]); "guidance_scale":"3.5"→json.Number("3.5"); "enable_safety_checker":"false"→false; "sync_mode":"TRUE"→true
func TestCoerceUnionObjectOrEnum(t *testing.T)   // image_size:"landscape_4_3" queda string; image_size:`{"width":512,"height":"768"}` → map con height json.Number("768") (ImageSize.height es integer)
func TestCoerceArrayFromRepeatAndJSON(t *testing.T) // kling i2v: "image_url" string queda; schema sintético {"properties":{"image_urls":{"type":"array","items":{"type":"string"}},"nums":{"type":"array","items":{"type":"integer"}}}}: "nums":["1","2"]→[json.Number...]; "nums":"[3,4]"→[3,4]; "nums":"7"→[json.Number("7")]
func TestCoerceLeavesUnconvertible(t *testing.T) // "num_images":"many" queda "many"; "seed":nil queda nil; "unknown_key":"5" queda "5"
func TestCoerceDoesNotMutate(t *testing.T)       // input y schema iguales tras la llamada (reflect.DeepEqual con copias)
func TestPropSchemaFindsInBranches(t *testing.T) // schema {"anyOf":[{"properties":{"a":{"type":"integer"}}}]}: PropSchema(.., "a") ok; "zz" no
```

- [ ] **Step 2: Run** `go test ./internal/schema/ -run 'Coerce|PropSchema'`. Expected: FAIL `undefined: schema.Coerce`.
- [ ] **Step 3: Implement** en `coerce.go` (puerto de kie-cli; `GuessLiterals` se queda como está).
- [ ] **Step 4: Run** `go test -race -cover ./internal/schema/`. Expected: PASS, ≥80%.
- [ ] **Step 5: Commit** `feat: coerce -p values to the endpoint input schema`

---

### Task 2: `schema.Validate` (JSON Schema draft 2020 con mensajes útiles)

**Files:**
- Create: `internal/schema/validate.go`
- Modify: `go.mod`, `go.sum` (`go get github.com/santhosh-tekuri/jsonschema/v6@v6.0.2 golang.org/x/text@v0.43.0`)
- Test: `internal/schema/validate_test.go`

**Interfaces:**
- Consumes: `schema.Doc.Input`, `schema.PropSchema` (Task 1).
- Produces:
  - `schema.Problem{Field string "field"; Message string "message"; Got any "got,omitempty"}`.
  - `schema.Validate(sch, input map[string]any) ([]Problem, error)`: compila `sch` como recurso `input.json` con draft 2020 por defecto; `error` solo si el schema no compila. Problemas ordenados por `Field` luego `Message`, sin duplicados, `nil` si válido. `Field` es ruta legible (`image_urls[0]`, `image_size.width`). `required` → un problema por campo faltante con `missing required field`. `additionalProperties:false` → `unknown field; see 'fal-cli models schema <endpoint>' for valid keys`. Claves de nivel superior no declaradas en un schema cerrado (o sin `additionalProperties` pero con `properties`) → el mismo mensaje una sola vez (`unknownFields`). Resto → `ErrorKind.LocalizedString(printer)` (inglés). **Puerto directo de kie-cli `internal/schema/validate.go`** cambiando el texto `kie-cli models schema <model>` por `fal-cli models schema <endpoint>`.
  - Nota: los valores `json.Number` que produce `Coerce` validan como números en jsonschema v6 (kie-cli ya depende de ello).

- [ ] **Step 1: Write the failing tests** (`sch := fetch(t, "fal-ai/flux/schnell").Input`, valores ya coercionados):

```go
func TestValidateOK(t *testing.T)                 // {"prompt":"x"} → nil, nil
func TestValidateRequired(t *testing.T)           // {} → [{Field:"prompt", Message:"missing required field"}]
func TestValidateRange(t *testing.T)              // {"prompt":"x","num_images":json.Number("99")} → 1 problema Field "num_images", Got json.Number("99"), Message contiene "4"
func TestValidateEnumUnion(t *testing.T)          // {"prompt":"x","image_size":"bogus"} → ≥1 problema con Field "image_size"; algún Message contiene "landscape_4_3"
func TestValidateNullableAnyOf(t *testing.T)      // {"prompt":"x","seed":nil} → nil; {"prompt":"x","seed":"abc"} → problema Field "seed"
func TestValidateUnknownField(t *testing.T)       // {"prompt":"x","promt":"y"} → exactamente 1 problema Field "promt", Message contiene "fal-cli models schema"
func TestValidateNestedPath(t *testing.T)         // {"prompt":"x","image_size":{"width":"wide"}} → problema Field "image_size.width"
func TestValidateArrayPath(t *testing.T)          // kling i2v (fetch "fal-ai/kling-video/v2.5-turbo/pro/image-to-video"): {"prompt":"x","image_url":"u","duration":"7"} → Field "duration" con Got "7" y Message contiene "5"; schema sintético {"properties":{"image_urls":{"type":"array","items":{"type":"string"},"maxItems":1}}}: {"image_urls":["a",2]} → Fields "image_urls" (maxItems) e "image_urls[1]" (type)
func TestValidateSchemaCompileError(t *testing.T) // {"type":"nope"} → err != nil
func TestValidateDoesNotMutate(t *testing.T)
```

- [ ] **Step 2: Run** `go test ./internal/schema/ -run Validate`. Expected: FAIL (build failed / undefined).
- [ ] **Step 3: Implement** `validate.go` (puerto) tras `go get` de las dos dependencias; `go mod tidy`.
- [ ] **Step 4: Run** `go test -race -cover ./internal/schema/` y `go mod tidy -diff`. Expected: PASS ≥80%, sin diff.
- [ ] **Step 5: Commit** `feat: validate input against the endpoint schema with readable problems`

---

### Task 3: Campos de archivo según el schema

**Files:**
- Modify: `internal/upload/upload.go` (`Resolve` recibe el predicado), `internal/upload/upload_test.go`
- Create: `internal/upload/filekey.go`, `internal/upload/filekey_test.go`

**Interfaces:**
- Consumes: `schema.PropSchema` (Task 1). Para no crear un ciclo de imports (`schema` no importa `upload`), `upload` importa `schema`.
- Produces:
  - `upload.SchemaFileKey(sch map[string]any, key string) bool`: `true` si `IsFileKey(key)` (sufijo `_url`/`_urls`), o si la propiedad (o sus `items`, atravesando `anyOf`/`oneOf` e ignorando ramas `null`) tiene `ui.field` ∈ `{image, video, audio, file}`, o si es `type: string` (o array de string) y su `description` contiene la palabra `URL` (regex `\bURL\b`, case-sensitive). `sch == nil` → solo el nombre.
  - `upload.Resolve(ctx, c, input, isFile func(string) bool, exists func(string) bool) (map[string]any, []string, error)`: firma nueva; `isFile` reemplaza la llamada interna a `IsFileKey`. `IsFileKey` se mantiene exportada (cli la usa para `pendingUploads`).
  - `// ponytail: three signals (name, ui.field, "URL" in description); stage 6 re-checks against every live schema.`

- [ ] **Step 1: Write the failing tests**:

```go
func TestSchemaFileKeyByName(t *testing.T)        // sch nil: "image_url" true, "prompt" false
func TestSchemaFileKeyByUIField(t *testing.T)     // sintético {"properties":{"mask":{"type":"string","ui":{"field":"image"}},"refs":{"type":"array","items":{"type":"string","ui":{"field":"image"}}},"tail":{"anyOf":[{"type":"string","ui":{"field":"video"}},{"type":"null"}]}}}: mask, refs, tail → true
func TestSchemaFileKeyByDescription(t *testing.T) // {"source":{"type":"string","description":"URL of the source file"}} → true; {"prompt":{"type":"string","description":"Describe the url scheme"}} → false (minúsculas); {"count":{"type":"integer","description":"URL count"}} → false (no string)
func TestSchemaFileKeyCalibration(t *testing.T)   // para los 47 fixtures de testdata/live/schemas: parsear con schema.Fetch vía el server de schema_test (o leer el JSON y resolver con schema.Resolve el *Input): toda clave con SchemaFileKey==true debe terminar en _url/_urls o tener ui.field; y ninguna de {"prompt","negative_prompt","text","seed"} da true. Imprime con t.Logf el total de claves de archivo detectadas (espera ≥ 60).
func TestResolveUsesPredicate(t *testing.T)       // Resolve con isFile que acepta "mask" sube ./a.png bajo "mask"; con IsFileKey no
```

  Actualizar los tests existentes de `Resolve` a la nueva firma (`upload.IsFileKey` como `isFile`).

- [ ] **Step 2: Run** `go test ./internal/upload/`. Expected: FAIL (firma / undefined).
- [ ] **Step 3: Implement** `filekey.go` y el cambio de firma; ajustar `internal/cli/run.go` para que compile (`upload.Resolve(ctx, c, input, upload.IsFileKey, fileExists)` por ahora; Task 4 pasa el predicado del schema).
- [ ] **Step 4: Run** `go test -race -cover ./...`. Expected: PASS ≥80%.
- [ ] **Step 5: Commit** `feat: detect file fields from the schema (ui.field, URL descriptions)`

---

### Task 4: Cableado en `run`: coerce → validate → upload

**Files:**
- Modify: `internal/cli/run.go`, `internal/cli/run_test.go`

**Interfaces:**
- Consumes: `schema.Coerce`, `schema.Validate`, `schema.PropSchema`, `upload.SchemaFileKey`, `upload.Resolve` (firma nueva).
- Produces (comportamiento de `run`):
  - `buildInput` devuelve el input crudo (`--input` < `-p`); los `-p` planos de claves **no declaradas** se tipan con `GuessLiterals` (igual que hoy); las declaradas las tipa `schema.Coerce(doc.Input, raw)`.
  - `prepareInput(cmd, deps, doc, endpoint, warn) (preparedInput, error)` con `preparedInput{input map[string]any; isFile func(string) bool; problems []schema.Problem}`: coerciona, valida con `schema.Validate(doc.Input, input)`; si hay problemas y no `--no-validate` → `&output.Error{Code: validation, Message: "input does not match the schema of <endpoint> (N problem(s)); fix the fields, or use --no-validate if the schema is wrong", Details: {"problems": problems}}`. Con `--no-validate`: `warning: validation: <field>: <message>` por problema en stderr y `problems` guardados. Si `Validate` devuelve `error` (schema no compila): `warning: cannot validate the input (<err>); sending without validation`. Valores `Got` string se recortan a 100 runas.
  - `--dry-run` emite `{"ok":true,"dryRun":true,"model","input":<coercionado>,"pendingUploads":[…],"price":{…}|null,"problems":[…]?}` (`problems` solo bajo `--no-validate` con problemas). El aviso actual de `--no-validate` ("is ignored") desaparece.
  - `pendingUploads` y `upload.Resolve` usan `upload.SchemaFileKey(doc.Input, k)`.
  - El resto del flujo (submit, wait, download) no cambia.

- [ ] **Step 1: Write the failing tests** en `run_test.go` (servidor `newFal` existente):

```go
func TestRunCoercesTypedParams(t *testing.T)      // run flux -p prompt=fox -p seed=5 -p enable_safety_checker=false -p image_size=landscape_4_3 -p num_images=2 --no-wait → f.submits[0]: seed 5.0, enable_safety_checker false, image_size "landscape_4_3", num_images 2.0
func TestRunObjectParamJSON(t *testing.T)         // -p image_size={"width":512,"height":512} --dry-run → input.image_size es map con width 512.0
func TestRunValidationErrorExit2(t *testing.T)    // -p prompt=fox -p num_images=99 → exit 2, error.code validation, details.problems[0].field "num_images", got 99.0; 0 submits, 0 uploads
func TestRunUnknownFieldIsValidation(t *testing.T) // -p promt=fox → exit 2, problems[].field contiene "promt" y "prompt" (missing required)
func TestRunNoValidateSendsWithWarning(t *testing.T) // -p prompt=fox -p num_images=99 --no-validate --no-wait → exit 0, submit con num_images 99, stderr contiene "warning: validation: num_images"
func TestRunDryRunReportsProblemsUnderNoValidate(t *testing.T) // --dry-run --no-validate con num_images=99 → m["problems"] len 1; sin --no-validate → exit 2
func TestRunUploadsUIFieldFile(t *testing.T)      // schema sintético servido por newFal para "fal-ai/fake/mask" (añadir al fake: si endpoint_id == "fal-ai/fake/mask" responde un openapi mínimo con Input {prompt:string, mask:{type:string, ui:{field:image}}} y Output {image:File}); -p prompt=x -p mask=<png local> --no-wait → f.uploads == 1 y submit.mask == "https://v3.fal.media/files/up.png"
func TestRunPromptPathNeverUploaded(t *testing.T) // -p prompt=<ruta existente> --no-wait → f.uploads == 0 y submit.prompt == la ruta
```

  Ajustar `TestRunInputFileAndParamOverride` (ya no espera el aviso "no-validate is ignored"; `--no-validate` sin problemas no escribe nada en stderr).

- [ ] **Step 2: Run** `go test ./internal/cli/ -run 'TestRun'`. Expected: FAIL (coerción/validación ausentes: `seed` llega como 5.0 ya por GuessLiterals, pero `num_images=99` no falla y `promt` no falla).
- [ ] **Step 3: Implement** en `run.go`.
- [ ] **Step 4: Run** `go test -race -cover ./...` y `make build`. Expected: PASS ≥80% por paquete.
- [ ] **Step 5: Smoke gratuito**: `set -a; . ./.env; set +a; bin/fal-cli run fal-ai/flux/schnell -p prompt="a red fox" -p num_images=99 --dry-run` → exit 2 con `problems`; `bin/fal-cli run fal-ai/flux/schnell -p prompt="a red fox" -p seed=3 --dry-run` → `input.seed` numérico. 0 USD.
- [ ] **Step 6: Commit** `feat: run coerces and validates the input before uploading or submitting`

---

## Fin de la etapa 2

Entregable: `run` tipa y valida contra el schema real; `--dry-run` enseña el body exacto; campos de archivo detectados por nombre, `ui.field` y descripción. Pendiente para etapas siguientes: ids ricos (3), presets + skill (4), distribución (5), verificación real (6).
