# Ship Only the Binary: `dist/`, Not Committed `web/` Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Rename the committed built-UI directory `web/` → `dist/`, untrack it, build it only inside Docker (`node` stage) or via `make ui`, and keep the final image binary-only.

**Architecture:** The Vite build outputs to `../dist`; `spa.go` embeds `dist`. `web/` is removed from git and both `dist/` and `web/` are gitignored. The Dockerfile node stage emits `/dist` and the Go stage copies it in before `go build`; the final scratch image is unchanged (binary + curl + certs). The Makefile `build`/`test`/`vet`/`run` targets gain a `ui` prerequisite so the embed always exists.

**Tech Stack:** Vite 5 (UI build), Go 1.24 (embed + build), Docker multi-stage, Make, git.

## Global Constraints

- No new Go module dependencies. `go.mod` keeps only `github.com/google/shlex` and `github.com/robfig/cron/v3`.
- `ui/vite.config.js` keeps `emptyOutDir: true` and the `/api` dev proxy.
- Final Docker image (`FROM scratch`) must contain only `/cronix`, `/usr/local/bin/curl`, and certs — no source, no `dist/`, no `web/`.
- `spa.go` must embed `dist`; SPA runtime behavior unchanged.
- Gate: `npm --prefix ui run build` clean → output in `dist/`; `go build ./... && go vet ./... && go test ./...` green (run after `make ui`); `gofmt -l .` clean; `git status` shows `dist/` and `web/` untracked.
- Commit `docs/superpowers/specs/2026-08-17-dist-build-no-web-commit-design.md` is already on the branch (design approved).

---

### Task 1: Retarget the Vite build output to `dist/` and update the embed

Point the UI build at `../dist`, move the Go embed from `web` to `dist`, and update README mentions. The Go build will still compile (an existing `web/` directory remains on disk for now).

**Files:**
- Modify: `ui/vite.config.js:7`
- Modify: `spa.go:9-13`
- Modify: `README.md` (two spots, exact strings below)

**Interfaces:**
- Consumes: nothing.
- Produces: Vite emits `dist/` instead of `web/`; `SPAHandler()` serves the embedded `dist`.

- [ ] **Step 1: Update `ui/vite.config.js`**

Replace line 12 (`outDir: '../web'`) with:

```js
    outDir: '../dist',
```

- [ ] **Step 2: Update `spa.go`**

Replace the embed directive and `fs.Sub`:

```go
//go:embed dist
var webFS embed.FS
```

```go
	sub, err := fs.Sub(webFS, "dist")
```

- [ ] **Step 3: Update README**

In `## Web UI`, replace:

```md
`go:embed` (in `spa.go`, serving the built `web/` directory).
```

with:

```md
`go:embed` (in `spa.go`, serving the built `dist/` directory).
```

In `## Building from Source`, replace:

```md
The built `web/` assets are committed, so a plain `go build` needs no Node
toolchain; only the Docker image (or `make ui`) invokes npm.
```

with:

```md
The built UI (`dist/`) is not committed. Run `make ui` before `make build`,
`make test`, or `make vet` so the Go toolchain has the embedded assets; the
Docker image builds the UI inside a Node stage automatically.
```

- [ ] **Step 4: Verify the UI builds to `dist/`**

Run: `npm --prefix ui run build`
Expected: output lines mention `dist/index.html` and `dist/assets/index-*.{js,css}`. Confirm: `ls dist/` lists `index.html` and `assets/`.

- [ ] **Step 5: Run the Go gate (web/ still present, so it compiles)**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: PASS (`go test` shows `ok cronix`).

- [ ] **Step 6: Commit**

```bash
git add ui/vite.config.js spa.go README.md
git commit -m "build: retarget UI build output to dist and update embed"
```

---

### Task 2: Untrack `web/`, gitignore `dist/`/`web/`, and finish the new layout

Remove `web/` from git, delete the working directory, ignore both directories, and wire the Makefile `ui` prerequisite so local gates keep working.

**Files:**
- Modify: `.gitignore`
- Modify: `Makefile` (help text + prerequisites)
- Delete (from disk + index): `web/`

**Interfaces:**
- Consumes: `dist/` output from Task 1.
- Produces: `make build|test|vet|run` depend on `ui`; `.gitignore` covers `dist/` and `web/`.

- [ ] **Step 1: Update `.gitignore`**

Add two lines (end of file):

```gitignore
/dist/
/web/
```

- [ ] **Step 2: Update the Makefile help text**

Replace line 20 (`build the React UI into web/ (npm ci + vite build)`) with:

```make
	@echo "  ui              build the React UI into dist/ (npm ci + vite build)"
```

- [ ] **Step 3: Add `ui` as a prerequisite to the Go targets**

Change the target lines:

```make
build:
	go build -o bin/$(BINARY) .
```

```make
test:
	go test ./...
```

```make
vet:
	go vet ./...
```

```make
run:
	mkdir -p $(DATA_DIR)
```

to:

```make
build: ui
	go build -o bin/$(BINARY) .
```

```make
test: ui
	go test ./...
```

```make
vet: ui
	go vet ./...
```

```make
run: ui
	mkdir -p $(DATA_DIR)
```

- [ ] **Step 4: Remove `web/` from git and disk**

Run: `git rm -r web`
Then: `rm -rf web`
Expected: `git status` shows `web/` deleted, not untracked.

- [ ] **Step 5: Verify a clean build via Make**

Run: `make build`
Expected: runs `npm --prefix ui ci`, `npm --prefix ui run build` (into `dist/`), then `go build -o bin/cronix .`. Exit 0.

- [ ] **Step 6: Verify the full gate and gitignore**

Run: `make vet && make test`
Expected: PASS.
Run: `git status --short`
Expected: `dist/` and `web/` do NOT appear (ignored).

- [ ] **Step 7: Commit**

```bash
git add .gitignore Makefile
git commit -m "build: untrack web bundle, ignore dist, make Go targets build UI first"
```

(Working-tree deletion of `web/` was staged by `git rm -r web` in Step 4; stage any remaining deletion with `git add -u web` if `git status` shows it unstaged.)

---

### Task 3: Docker — build `dist/` in the container, keep the image binary-only

Point the Dockerfile at `/dist` and keep the build context lean. The final `FROM scratch` stage is unchanged.

**Files:**
- Modify: `Dockerfile:37`
- Modify: `.dockerignore`

**Interfaces:**
- Consumes: `dist/` output naming from Task 1.
- Produces: `docker build` embeds the node-stage-built `dist/`; final image layers contain only binary + curl + certs.

- [ ] **Step 1: Update the Dockerfile copy**

Replace line 37 (`COPY --from=web /web ./web`) with:

```dockerfile
COPY --from=web /dist ./dist
```

- [ ] **Step 2: Update `.dockerignore`**

Append three lines:

```gitignore
dist
web
bin
```

- [ ] **Step 3: Verify the image builds and stays binary-only**

Run: `docker build -t cronix-dist-test .`
Expected: node stage builds; `go build` embeds `/dist`; final stage copies only `/cronix`, `/out/bin/curl`, certs.

Run: `docker create --name cronix-dist-inspect cronix-dist-test && docker export cronix-dist-inspect | tar -tv 2>/dev/null | grep -v -E '^.* (cronix|bin/curl|etc/ssl/certs/)' || true; docker rm cronix-dist-inspect`
Expected: only `/cronix`, `/usr/local/bin/curl`, `/etc/ssl/certs/...` entries (grep filter outputs nothing beyond those).

- [ ] **Step 4: Commit**

```bash
git add Dockerfile .dockerignore
git commit -m "build: embed dist built in container; keep final image binary-only"
```

---

### Task 4: Verify the end-to-end gate

Final full verification across UI, Go, and git state.

**Files:**
- None (verification only).

- [ ] **Step 1: Rebuild everything from a clean state**

Run: `npm --prefix ui run build && go build ./... && go vet ./... && go test ./...`
Expected: all pass.

- [ ] **Step 2: Confirm formatting and repo state**

Run: `gofmt -l .`
Expected: no output.
Run: `git status --short`
Expected: no tracked changes; `dist/` and `web/` absent (ignored).

- [ ] **Step 3: Confirm `make test` still works standalone**

Run: `make test`
Expected: PASS (runs `ui` prerequisite then `go test ./...`).

- [ ] **Step 4: Confirm SPA tests serve the built UI**

Run: `go test -run 'TestSpaServes' ./...`
Expected: PASS (proves the embedded `dist/` is served).
