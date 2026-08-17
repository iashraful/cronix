# Cronix — Ship Only the Binary: `dist/`, Not Committed `web/`

Date: 2026-08-17
Status: Approved

## Problem

The built UI bundle (`web/`) is committed to git. The user wants it renamed to
`dist/`, **not** committed, and generated only where builds happen (Docker, and
locally via `make ui`). The Docker image should contain only the binary (and its
runtime deps: curl, certs) — no source frontend/backend files.

Today the final image already is `FROM scratch` with only `/cronix`, curl, and
certs; but the built UI is checked in, and the README/Makefile contract says a
plain `go build` needs no Node.

## Decisions (from brainstorming)

- Rename the built UI output directory `web/` → `dist/`.
- Untrack `web/`; gitignore `dist/` and `web/`.
- `go:embed` moves from `web` to `dist`.
- Docker continues to build the UI in a `node` stage and embed it into the
  binary; the final image is **unchanged** (scratch + binary + curl + certs).
- Local `build`, `test`, `vet`, `run` Makefile targets gain a `ui` prerequisite
  so the embed always exists before the Go toolchain runs. The old contract
  "plain `go build` needs no Node" is replaced by "build the UI first with
  `make ui` (or let Docker do it)".

## Design

### Build output (`ui/vite.config.js`)

- `build.outDir`: `'../web'` → `'../dist'` (`emptyOutDir` stays true).

### Embed (`spa.go`)

- `//go:embed dist`
- `fs.Sub(webFS, "dist")`

### Git

- `git rm -r --cached web`; remove the working `web/` directory.
- `.gitignore`: add `dist/` and `web/`.

### Dockerfile

- Node stage: `WORKDIR /ui`, `COPY ui/ ./`, `npm ci && npm run build` → emits
  `/dist` (via `../dist`). Unchanged except the output name.
- Go stage: `COPY --from=web /dist ./dist` (was `/web ./web`).
- Final `FROM scratch` stage unchanged — still only binary + curl + certs.

### `.dockerignore`

- Add `dist`, `web`, `bin` so the `COPY . .` build context stays minimal.

### Makefile

- `ui` help text: `into web/` → `into dist/`.
- `build`, `test`, `vet`, `run`: add `ui` to `.PHONY` prerequisites line and as
  a prerequisite so the `dist/` embed exists. (`run`, `build`, `test`, `vet`
  become `run: ui`, etc.) Keep `ui` itself unchanged (`npm --prefix ui ci` +
  build).

### README

- Update "Building from Source": remove the claim that committed `web/` assets
  mean `go build` needs no Node; say the UI must be built first via `make ui`
  (or the Docker image builds it inside the container).
- Update the Web UI embed mention (`spa.go`, serving the built `web/` directory
  → `dist/`).

## Testing

- `npm --prefix ui run build` clean; output lands in `dist/`.
- `go build ./... && go vet ./... && go test ./...` green after `make ui`.
- `gofmt -l .` clean.
- `git status` shows no tracked `dist/` or `web/`; `dist/` ignored.
- Docker: `docker build -t cronix .` succeeds; final image layers contain only
  `/cronix`, `/usr/local/bin/curl`, certs.

## Out of scope

- No change to the SPA runtime behavior, routes, or API.
- No multi-image or multi-arch changes.