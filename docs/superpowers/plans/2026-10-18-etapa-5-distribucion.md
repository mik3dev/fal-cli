# fal-cli — Etapa 5: Distribución — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Binarios reproducibles con goreleaser, CI que ejecuta tests y lint sin tocar la API real, y un README que deja a una persona instalando y generando en cinco minutos.

**Architecture:** Copia adaptada de kie-cli: `.goreleaser.yaml` (linux/darwin/windows × amd64/arm64, `CGO_ENABLED=0`, versión inyectada en `internal/cli.Version`), `.github/workflows/{ci,release}.yml`, y un test en la raíz (`workflows_test.go`, package `falcli`) que garantiza que ningún workflow usa la key real, el build tag `live` ni secretos distintos de `GITHUB_TOKEN`.

**Tech Stack:** goreleaser v2, GitHub Actions (`actions/checkout@v7`, `actions/setup-go@v7`, `golangci/golangci-lint-action@v9` v2.15.0 `install-mode: goinstall`, `goreleaser/goreleaser-action@v7`).

**Spec:** `docs/superpowers/specs/2026-10-17-fal-cli-design.md` (§8 etapa 5, §9 "Smoke reales: solo a mano").

## Global Constraints

- CI nunca usa `FAL_API_KEY`, `FAL_KEY`, `FAL_E2E*` ni el build tag `live`. Sin `secrets.*` salvo `GITHUB_TOKEN`.
- `make build` sigue inyectando `Version` con `git describe`.
- El README no contiene la key ni rutas locales.
- `go test -race ./...`, `go vet`, `gofmt -l` limpios antes de cada commit.

## Review Focus

1. **Workflow que añade un secreto** (`secrets.FAL_KEY`): el test de guardia falla nombrando el archivo. → test en Task 1.
2. **Versión no inyectada** (`fal-cli version` imprime `dev` en un binario de release): el check de CI "Version injection" falla. → paso en Task 1 (ci.yml) + `TestGoreleaserInjectsVersion`.
3. **`go.mod` no tidy** tras añadir `yaml.v3`: CI falla en "Check go.mod is tidy". → paso en Task 1.
4. **README con comandos que no existen** (`models health`): el README se incluye en el test de consistencia de la skill. → test en Task 2.
5. **Windows**: `GOOS=windows go vet ./...` en CI; `skills install` sin `HOME` usa `USERPROFILE`. → paso en Task 1.

---

### Task 1: goreleaser, CI y guardia de workflows

**Files:**
- Create: `.goreleaser.yaml`, `.github/workflows/ci.yml`, `.github/workflows/release.yml`, `workflows_test.go` (package `falcli`, junto a `assets.go`)

**Interfaces:**
- Produces: `.goreleaser.yaml` con `project_name: fal-cli`, `main: ./cmd/fal-cli`, ldflags `-s -w -X github.com/mik3dev/fal-cli/internal/cli.Version={{.Version}}`, archivos `fal-cli_<version>_<os>_<arch>` (`tar.gz`; `zip` en windows), `checksums.txt`. `ci.yml`: tidy check, `go test -race -count=1 -cover ./...`, golangci-lint, `GOOS=windows go vet`, `GOOS=darwin go build`, version injection check (`"version":"ci-check"`), `goreleaser check`. `release.yml` en tags `v*`.

- [ ] **Step 1: Write the failing tests** (`workflows_test.go`, puerto de kie-cli con `FAL_API_KEY`, `FAL_KEY`, `FAL_E2E` como substrings prohibidos):

```go
func TestWorkflowGuardRejectsBadSnippets(t *testing.T)
func TestWorkflowsNeverUseLiveKey(t *testing.T)   // ≥2 workflows, 0 violaciones
func TestGoreleaserInjectsVersion(t *testing.T)  // contiene internal/cli.Version={{.Version}}, windows, darwin, linux, arm64, CGO_ENABLED=0
```

- [ ] **Step 2: Run** `go test ./`. Expected: FAIL (archivos ausentes).
- [ ] **Step 3: Implement** los tres YAML.
- [ ] **Step 4: Run** `go test -race ./... && go mod tidy -diff && (command -v goreleaser >/dev/null && goreleaser check || echo "goreleaser not installed: skipped")`. Expected: PASS; tidy sin diff.
- [ ] **Step 5: Commit** `ci: goreleaser config, test+lint workflow and release workflow`

---

### Task 2: README

**Files:**
- Create: `README.md`
- Modify: `internal/cli/skills_consistency_test.go` (añadir README al test de comandos reales)

**Interfaces:**
- Produces: `README.md` con secciones Install (release archive vía `gh release download`, `go install` con `GOPRIVATE`, `make install`), Setup (`auth login|status`, `FAL_API_KEY`/`FAL_KEY`, `~/.fal-cli/`, `FAL_CLI_HOME`; `credits` requiere key con permiso BILLING), Quickstart (`models search` → `models schema` → `models price` → `run --dry-run` → `run`), Presets (tabla de los 10 con modelo y precio orientativo; `~/.fal-cli/presets.yaml`), Agent skill (`skills install`), Output and exit codes (tabla de 10 códigos), Development (`make test`, `make lint`, smoke reales con `FAL_E2E=1`, `FAL_E2E_BUDGET`, `set -a; . ./.env; set +a`; nunca en CI).
  - `TestReadmeMentionsOnlyRealCommands`: `checkSkillText` sobre `../../README.md` → 0 problemas.

- [ ] **Step 1: Write the failing test** `TestReadmeMentionsOnlyRealCommands` (README ausente → `t.Fatal`).
- [ ] **Step 2: Run** `go test -run Readme ./internal/cli/`. Expected: FAIL.
- [ ] **Step 3: Write** `README.md`.
- [ ] **Step 4: Run** `go test -race ./...`. Expected: PASS.
- [ ] **Step 5: Commit** `docs: README with install, setup, presets, skill and exit codes`

---

## Fin de la etapa 5

Entregable: release reproducible, CI verde sin API real, README. Pendiente: verificación real (6).
