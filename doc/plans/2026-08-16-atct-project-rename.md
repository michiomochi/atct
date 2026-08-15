# Namespace → Project Rename and Project CLI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Rename the Namespace concept to Project throughout the code, and add the missing CLI that lets a human register one.

**Architecture:** The rename is mechanical and must land in one commit because Go will not compile with a partially renamed type. On top of it, `store` gains `ListProjects` and a public `NormalizeRoot`, the daemon gains `project.list`, and `atct` gains `project add` / `project list`. Without that CLI there is no way to create the first project, so ATCT cannot be used at all.

**Tech Stack:** Go 1.26 / SQLite (`modernc.org/sqlite`) / the standard `testing` package.

**Spec:** `doc/specs/2026-08-15-atct-design.md` (updated to say Project)

## Global Constraints

- The rename is **naming only**. Behavior does not change: an arbitrary `root_path`, nesting allowed with longest-prefix match, manual registration.
- **No database migration.** ATCT is unreleased and the local database holds zero projects. The schema is recreated under the new table name and `~/.atct/atct.db` is deleted once.
- **`doc/plans/2026-08-15-atct-core.md` is not rewritten.** It is the record of a completed plan and already differs from the code in other ways. The current design lives in the spec, which is already updated.
- Every identifier changes together: `Namespace` → `Project`, `namespace` → `project`, `namespaces` → `projects`, `namespace_id` → `project_id`, `ErrNamespaceNotFound` → `ErrProjectNotFound`.
- The MCP tool contract stays at **eight tools**. Project creation is a human action and does not become a ninth tool.
- The Web UI v1 scope does not grow. Project creation does not get a screen.
- Do not run `git push`, create a release, or publish anything.

---

## File Structure

| File | Responsibility |
|---|---|
| `internal/domain/model.go` | `Project` struct; `Goal.ProjectID` |
| `internal/store/schema.go` | `projects` table; `goals.project_id` |
| `internal/store/namespace.go` → `internal/store/project.go` | Project creation, resolution, listing, root normalization |
| `internal/store/goal.go` | `project_id` column references |
| `internal/daemon/handler.go` | `project.create`, `project.list` |
| `internal/httpapi/server.go` | `project_id` in JSON |
| `web/src/` | `project_id` reference |
| `cmd/atct/main.go` | `project add` / `project list` subcommands |

---

### Task 1: Mechanical rename

**Files:**
- Rename: `internal/store/namespace.go` → `internal/store/project.go`
- Rename: `internal/store/namespace_test.go` → `internal/store/project_test.go`
- Modify: every file under `internal/` and `web/src/` that mentions namespace
- Test: the existing suite, renamed with the code

**Interfaces:**
- Consumes: nothing new
- Produces: `domain.Project` (fields `ID`, `Name`, `RootPath`, `CreatedAt`); `domain.Goal.ProjectID`; `store.CreateProject(ctx, name, rootPath string) (domain.Project, error)`; `store.ResolveProject(ctx, cwd string) (domain.Project, error)`; `store.ErrProjectNotFound`; daemon methods `project.create`

- [ ] **Step 1: Confirm the current state**

```bash
grep -ril 'namespace' internal web/src | sort
grep -rio 'namespace' internal web/src | wc -l    # expect 100
go test ./... -race                                # expect PASS before the change
```

- [ ] **Step 2: Rename the two files**

```bash
git mv internal/store/namespace.go internal/store/project.go
git mv internal/store/namespace_test.go internal/store/project_test.go
```

- [ ] **Step 3: Replace every occurrence**

```bash
grep -rl 'Namespace\|namespace' internal web/src \
  | xargs sed -i '' 's/Namespace/Project/g; s/namespace/project/g'
```

This is intentionally blunt. `namespaces` becomes `projects` and `namespace_id`
becomes `project_id` because the substring matches. Error strings inside tests
change with the code they assert on, which is correct — the message
`namespace not found for cwd` becomes `project not found for cwd`.

- [ ] **Step 4: Verify nothing was missed and nothing was mangled**

```bash
grep -ri 'namespace' internal web/src    # expect no output
go build ./...
go vet ./...
```

**Read the diff before continuing.** A blunt replace can produce wording that
no longer reads correctly in comments and doc strings — for example a sentence
that used both words to contrast them. Fix any such sentence by hand. Report
every hand fix.

- [ ] **Step 5: Run the suite**

```bash
go test ./... -race -count=2
```

Expected: PASS. The rename must not change behavior.

- [ ] **Step 6: Commit**

```bash
git add -u internal web/src
git commit -m "refactor: rename namespace to project"
```

---

### Task 2: Listing and root normalization in the store

**Files:**
- Modify: `internal/store/project.go`
- Modify: `internal/store/project_test.go`

**Interfaces:**
- Consumes: `store.CreateProject`
- Produces: `(*Store) ListProjects(ctx context.Context) ([]domain.Project, error)`; `store.NormalizeRoot(ctx context.Context, path string) string`

- [ ] **Step 1: Write the failing test**

Append to `internal/store/project_test.go`:

```go
func TestListProjectsReturnsAllInCreationOrder(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	if _, err := s.CreateProject(ctx, "first", "/repos/first"); err != nil {
		t.Fatalf("CreateProject first: %v", err)
	}
	if _, err := s.CreateProject(ctx, "second", "/repos/second"); err != nil {
		t.Fatalf("CreateProject second: %v", err)
	}

	got, err := s.ListProjects(ctx)
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ListProjects returned %d projects, want 2", len(got))
	}
	if got[0].Name != "first" || got[1].Name != "second" {
		t.Fatalf("unexpected order: %q then %q", got[0].Name, got[1].Name)
	}
}

func TestListProjectsIsEmptyWhenNoneExist(t *testing.T) {
	got, err := newTestStore(t).ListProjects(context.Background())
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("ListProjects returned %d projects, want 0", len(got))
	}
}

func TestNormalizeRootMapsWorktreeToMainRepository(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is unavailable")
	}
	ctx := context.Background()

	mainRepo := filepath.Join(t.TempDir(), "main")
	worktree := filepath.Join(t.TempDir(), "worktree")
	if err := os.MkdirAll(mainRepo, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	runGit := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	runGit(mainRepo, "init", "-q")
	if err := os.WriteFile(filepath.Join(mainRepo, "README"), []byte("x\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	runGit(mainRepo, "add", "README")
	runGit(mainRepo, "-c", "user.name=T", "-c", "user.email=t@example.com", "commit", "-qm", "init")
	runGit(mainRepo, "worktree", "add", "-q", worktree, "HEAD")

	got := NormalizeRoot(ctx, worktree)
	want, err := filepath.EvalSymlinks(mainRepo)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	gotResolved, err := filepath.EvalSymlinks(got)
	if err != nil {
		t.Fatalf("EvalSymlinks(got): %v", err)
	}
	if gotResolved != want {
		t.Fatalf("NormalizeRoot = %q, want %q", gotResolved, want)
	}
}
```

Add `"os"`, `"os/exec"`, and `"path/filepath"` to the imports if they are not
already there.

- [ ] **Step 2: Confirm that the test fails**

Run: `go test ./internal/store/ -run 'TestListProjects|TestNormalizeRoot' -v`
Expected: FAIL (`undefined: ListProjects`, `undefined: NormalizeRoot`)

- [ ] **Step 3: Write the implementation**

Append to `internal/store/project.go`:

```go
// NormalizeRoot exposes the worktree normalization that ResolveProject already
// performs internally. Registration must use the same rule as resolution: a
// project registered from inside a worktree, but stored under the worktree's
// own path, would never resolve again.
func NormalizeRoot(ctx context.Context, path string) string {
	return normalizeWorktreePath(ctx, path, "git")
}

func (s *Store) ListProjects(ctx context.Context) ([]domain.Project, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, root_path, created_at FROM projects ORDER BY created_at`)
	if err != nil {
		return nil, fmt.Errorf("query projects: %w", err)
	}
	defer rows.Close()

	out := []domain.Project{}
	for rows.Next() {
		var p domain.Project
		var createdAt string
		if err := rows.Scan(&p.ID, &p.Name, &p.RootPath, &createdAt); err != nil {
			return nil, fmt.Errorf("scan project: %w", err)
		}
		t, err := time.Parse(time.RFC3339, createdAt)
		if err != nil {
			return nil, fmt.Errorf("parse created_at: %w", err)
		}
		p.CreatedAt = t
		out = append(out, p)
	}
	return out, rows.Err()
}
```

`out` is initialized to an empty slice rather than left nil so the JSON
encoding is `[]` and not `null`, matching the rule the HTTP API already follows.

- [ ] **Step 4: Confirm that the tests pass**

Run: `go test ./internal/store/ -race -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/store/project.go internal/store/project_test.go
git commit -m "feat(store): list projects and expose root normalization"
```

---

### Task 3: Expose project listing over the daemon

**Files:**
- Modify: `internal/daemon/handler.go`
- Modify: `internal/daemon/server_test.go`

**Interfaces:**
- Consumes: `store.ListProjects`, `store.NormalizeRoot`, `store.CreateProject`
- Produces: the daemon method `project.list`; `project.create` normalizing its `root_path` before insert

- [ ] **Step 1: Write the failing test**

Append to `internal/daemon/server_test.go`, following the shape of the existing
round-trip test in that file:

```go
// newDaemonConn starts a daemon on a fresh socket and returns a connection to
// it. The existing TestDaemonRoundTrip inlines this; extracting it here keeps
// the new tests from repeating the dial-retry loop.
func newDaemonConn(t *testing.T) net.Conn {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "atct.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	sock := filepath.Join(dir, "atct.sock")
	go New(s).Serve(ctx, sock)

	var conn net.Conn
	for i := 0; i < 50; i++ {
		conn, err = net.Dial("unix", sock)
		if err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

// call sends one request and reads one response. The reader is created per
// call because each test issues its requests sequentially on the same
// connection.
func call(t *testing.T, conn net.Conn, method string, params any) rpc.Response {
	t.Helper()
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	req, err := json.Marshal(rpc.Request{Method: method, Params: raw})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	if _, err := conn.Write(append(req, '\n')); err != nil {
		t.Fatalf("write %s: %v", method, err)
	}
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		t.Fatalf("read %s: %v", method, err)
	}
	var resp rpc.Response
	if err := json.Unmarshal(line, &resp); err != nil {
		t.Fatalf("unmarshal %s: %v", method, err)
	}
	return resp
}

func TestDaemonListsProjects(t *testing.T) {
	conn := newDaemonConn(t)

	created := call(t, conn, "project.create", map[string]string{
		"name": "atct", "root_path": "/repos/atct",
	})
	if created.Error != "" {
		t.Fatalf("project.create: %s", created.Error)
	}

	listed := call(t, conn, "project.list", map[string]string{})
	if listed.Error != "" {
		t.Fatalf("project.list: %s", listed.Error)
	}

	var projects []domain.Project
	if err := json.Unmarshal(listed.Result, &projects); err != nil {
		t.Fatalf("unmarshal projects: %v", err)
	}
	if len(projects) != 1 {
		t.Fatalf("project.list returned %d projects, want 1", len(projects))
	}
	if projects[0].Name != "atct" {
		t.Fatalf("name = %q, want %q", projects[0].Name, "atct")
	}
}

func TestDaemonListsProjectsWhenNoneExist(t *testing.T) {
	resp := call(t, newDaemonConn(t), "project.list", map[string]string{})
	if resp.Error != "" {
		t.Fatalf("project.list on an empty store: %s", resp.Error)
	}

	var projects []domain.Project
	if err := json.Unmarshal(resp.Result, &projects); err != nil {
		t.Fatalf("unmarshal projects: %v", err)
	}
	if len(projects) != 0 {
		t.Fatalf("got %d projects, want 0", len(projects))
	}
}
```

Add `"github.com/michiomochi/atct/internal/domain"` to the imports. The other
imports the helpers need — `bufio`, `context`, `encoding/json`, `net`,
`path/filepath`, `testing`, `time`, `rpc`, `store` — are already present.

`TestDaemonRoundTrip` stays as it is; do not rewrite it to use the helpers.
Changing a passing test while adding new ones makes a failure ambiguous.

- [ ] **Step 2: Confirm that the test fails**

Run: `go test ./internal/daemon/ -run TestDaemonListsProjects -v`
Expected: FAIL (unknown method `project.list`)

- [ ] **Step 3: Register the method**

Add a `case "project.list":` to the handler switch. It takes no parameters and
returns the slice from `Store.ListProjects`.

In the existing `case "project.create":`, pass the incoming path through
`store.NormalizeRoot` before calling `Store.CreateProject`, so a project
registered from inside a worktree is stored under the main repository's path.

- [ ] **Step 4: Confirm that the tests pass**

Run: `go test ./internal/daemon/ -race -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/daemon/
git commit -m "feat(daemon): add project.list and normalize project roots"
```

---

### Task 4: The `atct project` subcommand

**Files:**
- Modify: `cmd/atct/main.go`
- Modify: `cmd/atct/main_test.go`

**Interfaces:**
- Consumes: the daemon methods `project.create` and `project.list`; `daemonctl.Ensure`
- Produces: `atct project add [name]`; `atct project list`

- [ ] **Step 1: Write the failing test**

Append to `cmd/atct/main_test.go`:

```go
func TestParseArgsAcceptsProjectAdd(t *testing.T) {
	cfg, err := parseArgs([]string{"project", "add", "myproj"})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if cfg.subcommand != "project" {
		t.Fatalf("subcommand = %q, want %q", cfg.subcommand, "project")
	}
	if cfg.projectAction != "add" {
		t.Fatalf("projectAction = %q, want %q", cfg.projectAction, "add")
	}
	if cfg.projectName != "myproj" {
		t.Fatalf("projectName = %q, want %q", cfg.projectName, "myproj")
	}
}

func TestParseArgsAcceptsProjectAddWithoutName(t *testing.T) {
	cfg, err := parseArgs([]string{"project", "add"})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if cfg.projectName != "" {
		t.Fatalf("projectName = %q, want empty so the caller derives it", cfg.projectName)
	}
}

func TestParseArgsAcceptsProjectList(t *testing.T) {
	cfg, err := parseArgs([]string{"project", "list"})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if cfg.projectAction != "list" {
		t.Fatalf("projectAction = %q, want %q", cfg.projectAction, "list")
	}
}

func TestParseArgsRejectsUnknownProjectAction(t *testing.T) {
	if _, err := parseArgs([]string{"project", "destroy"}); err == nil {
		t.Fatal("expected an error for an unknown project action")
	}
}

func TestParseArgsRejectsProjectWithoutAction(t *testing.T) {
	if _, err := parseArgs([]string{"project"}); err == nil {
		t.Fatal("expected an error when no project action is given")
	}
}
```

- [ ] **Step 2: Confirm that the test fails**

Run: `go test ./cmd/atct/ -run TestParseArgsAcceptsProject -v`
Expected: FAIL (`project` is rejected as an unknown subcommand)

- [ ] **Step 3: Extend the parser**

Replace `cliConfig`, `validSubcommands`, `printUsage`, and `parseArgs` with
these. The current `parseArgs` rejects any positional argument after the
subcommand, which is why `project add` needs its own branch before the flag
parsing.

```go
type cliConfig struct {
	subcommand    string
	listenAddr    string
	projectAction string
	projectName   string
}

var validSubcommands = map[string]bool{
	"daemon": true, "ensure": true, "stop": true, "project": true,
}

var validProjectActions = map[string]bool{"add": true, "list": true}

func printUsage() {
	fmt.Fprintln(os.Stderr, "Usage: atct <command> [options]")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Commands:")
	fmt.Fprintln(os.Stderr, "  daemon          Run the ATCT daemon in the foreground")
	fmt.Fprintln(os.Stderr, "  ensure          Start the daemon if it is not already running")
	fmt.Fprintln(os.Stderr, "  stop            Stop the running daemon")
	fmt.Fprintln(os.Stderr, "  project add     Register the current directory as a project")
	fmt.Fprintln(os.Stderr, "  project list    List registered projects")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Options:")
	fmt.Fprintln(os.Stderr, "  -listen string   HTTP listen address (default \"127.0.0.1:8787\")")
}

func parseArgs(args []string) (cliConfig, error) {
	if len(args) < 1 {
		printUsage()
		return cliConfig{}, errInvalidArgs
	}
	sub := args[0]
	if !validSubcommands[sub] {
		fmt.Fprintf(os.Stderr, "unknown subcommand %q\n", sub)
		printUsage()
		return cliConfig{}, errInvalidArgs
	}

	rest := args[1:]
	cfg := cliConfig{subcommand: sub}

	if sub == "project" {
		if len(rest) < 1 {
			fmt.Fprintln(os.Stderr, "project requires an action: add or list")
			printUsage()
			return cliConfig{}, errInvalidArgs
		}
		action := rest[0]
		if !validProjectActions[action] {
			fmt.Fprintf(os.Stderr, "unknown project action %q\n", action)
			printUsage()
			return cliConfig{}, errInvalidArgs
		}
		cfg.projectAction = action
		rest = rest[1:]

		if action == "add" && len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
			cfg.projectName = rest[0]
			rest = rest[1:]
		}
	}

	flags := flag.NewFlagSet(sub, flag.ExitOnError)
	flags.SetOutput(os.Stderr)
	flags.Usage = printUsage
	listenAddr := flags.String("listen", defaultListenAddr, "HTTP listen address")
	flags.Parse(rest)
	if len(flags.Args()) > 0 {
		fmt.Fprintf(os.Stderr, "unexpected argument %q\n", flags.Args()[0])
		printUsage()
		return cliConfig{}, errInvalidArgs
	}

	cfg.listenAddr = *listenAddr
	return cfg, nil
}
```

Add `"strings"` to the imports. The `strings.HasPrefix` guard keeps
`atct project add -listen X` from consuming the flag as the project name.

- [ ] **Step 4: Wire the actions**

Both actions need a running daemon, so both call `daemonctl.Ensure` first,
exactly as `ensure` does.

`project add`:

1. Resolve the working directory with `os.Getwd()`.
2. Call `project.create` over the socket with that path and the name. When the
   name is empty, the daemon side already has the normalized path — derive the
   name from `filepath.Base` of the **normalized** path by asking for it in the
   response rather than guessing locally.
3. On success print `registered project "<name>" at <path>` to **stderr**.
4. **When the project already exists, print `already registered as "<name>"` to
   stderr and exit 0.** Running `atct project add` twice is something a user
   does; it is not an error, for the same reason `atct stop` twice is not.

`project list`: call `project.list` and print one line per project to stdout in
the form `<name>\t<root_path>`. **This is the one place stdout is correct** —
it is a list a user may pipe into another command, and `atct` itself never
speaks the MCP protocol.

- [ ] **Step 5: Verify**

```bash
go build ./... && go vet ./... && go test ./... -race
```

- [ ] **Step 6: Commit**

```bash
git add cmd/atct/
git commit -m "feat(cli): add project add and project list"
```

---

### Task 5: Recreate the database and verify end to end

**Files:**
- None. This task only verifies.

**Interfaces:**
- Consumes: everything above
- Produces: nothing

- [ ] **Step 1: Stop the daemon and delete the old database**

The old database has `namespaces` tables that the new code does not know about.
It holds zero projects, so nothing is lost.

```bash
atct stop
rm ~/.atct/atct.db ~/.atct/atct.db-shm ~/.atct/atct.db-wal
```

Use file-by-file `rm`. Do not use `rm -rf`.

- [ ] **Step 2: Register this repository as a project**

```bash
atct project add atct
atct project list
```

Expected: the second command prints `atct` and this repository's path.

- [ ] **Step 3: Confirm idempotence**

```bash
atct project add atct; echo "exit=$?"
```

Expected: `already registered`, `exit=0`.

- [ ] **Step 4: Confirm the schema**

```bash
sqlite3 ~/.atct/atct.db ".tables"
```

Expected: `projects` appears and `namespaces` does not. If `sqlite3` is
unavailable, skip this step and record that it was skipped.

- [ ] **Step 5: Confirm resolution works end to end**

```bash
curl -s http://127.0.0.1:8787/api/inbox
```

Expected: JSON with `active_goals` present and no error.

- [ ] **Step 6: Report**

No commit. Report the output of every step above.

---

## Verification for the whole plan

```bash
grep -ri 'namespace' internal cmd web/src    # no output
go build ./...
go vet ./...
go test ./... -race -count=2
atct project list
```
