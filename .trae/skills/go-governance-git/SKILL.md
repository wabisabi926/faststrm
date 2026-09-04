---
name: "go-governance-git"
description: "Standardizes the Go repo gofmt → multi-file logical-group conventional commit → push origin go workflow, with Windows PowerShell 5 anti-pit guidelines. Invoke when user says commit & push changes on this Go repo, after a fix/test/refactor pass is done locally and verified."
---

# Go Governance Git (Workspace)

Standard 6-step SOP for pushing working-tree verified changes to the `go` branch of this repository. Works with the Windows PowerShell 5 host that ships by default with the TRAE workspace (UTF-8 BOM on disk, arg-splitting on whitespace inside double-quoted strings).

## When to activate

Trigger when the user explicitly asks to "入库并 push go 分支", "提交并推送 go", "commit & push", or any Chinese/English phrase equivalent to "I just verified the fix/test locally, now land it in origin/go".

Do **not** activate for:
- Local-only investigations or diff previews without a "push" intent.
- Tag creation / release workflows (those use tag + release.yml separately).
- Untracked files the user has asked to keep local (e.g. throwaway smoke tests that should NOT enter git — always confirm scope before staging).

## 6-step SOP (execute in order)

### Step 1. Re-run final verification (never skip)

```
go build ./...
go test -count=1 ./internal/handler/... ./internal/server/... ./internal/service/strm/... ./internal/service/embyproxy/...
```

Both must exit 0 before touching git. If any package FAILs, stop and report back. Do **not** partially push a green test set with a known failing package.

### Step 2. gofmt every modified / new .go file

Collect every `*.go` file shown by `git status --porcelain` (both `M` and `??`):

```
git status --porcelain                                    # list targets
gofmt -w <path1.go> <path2.go> ... <pathN.go>             # one gofmt call per file-group of ~20 max
```

Windows caveat: `gofmt -w internal/handler/*.go` is fine if only one dir changed; prefer explicit file paths when files span many packages (avoids walking vendor / generated dirs).

### Step 3. Analyze changes → logical grouping (1 commit ≡ 1 semantic change)

Run:

```
git diff               # on already-tracked files with M
git diff --no-index /dev/null <untracked-file>   # for each ?? file (or just `cat <??.go>` / read it)
```

Decide conventional commit per group using the project's agreed type vocabulary:

| Type | Use when |
|------|----------|
| `fix(handler)` | Bug fix in `internal/handler/*` that changes runtime behavior (e.g. missing parse for multipart, broken nil-vs-empty branch). |
| `fix(service/<scope>)` | Bug fix deeper in services (store/task/monitor/emby/etc.). Use the narrowest scope directory name. |
| `test(handler)` / `test(service/<scope>)` | Pure new tests / new matrix coverage without any runtime logic change. If a test is added together with the `fix` that makes it pass, **split into two commits**: first `fix(...)` land runtime change, then `test(...)` land the new matrix. This lets bisect land fix-only and keeps per-commit diffs focused. |
| `feat(<scope>)` | New user-visible capability (new API route, new Emby setting column, new task mode). |
| `refactor(<scope>)` | Behavior-preserving cleanup (rename, extract helper). Must not change observable output. |
| `build(ci)` / `chore(ci)` | Workflow yml / scripts only. |

**Hard rule: a single commit MUST NOT mix `fix` runtime logic with `test` additions, UNLESS the test is <= 10 lines AND the fix would be un-reason-able standalone.** This repo has already reaped the benefit of split commits (e.g. 2026-09-04 multipart fix vs bool matrix): CI green-check on the `fix` commit alone proves the existing test suite didn't regress, then the `test` commit proves the new scenario is now covered.

### Step 4. Stage & commit — one conventional commit per group

**CRITICAL Windows PowerShell 5 rule**: commit messages MUST be **single-line, wrapped in single quotes**. Do NOT pass multi-line `-m` bodies with embedded newlines inside double quotes: PS5 will tokenize any whitespace/newline it finds and pass the fragments as separate positional args, producing errors like `error: pathspec '→' did not match any file(s) known to git`.

If a body / footer is really required, use a scratch temp-file and `-F`:

```powershell
# Prefer single-line:
git add <group-files>
git commit -m 'fix(handler): fillUpsertFromBody parse multipart before FormValue'

# Only use -F if multi-line body is mandatory:
$msg = "type(scope): one-line summary`n`nextra paragraph body text here, no arrow glyphs"
[System.IO.File]::WriteAllText("$env:TEMP\git-msg.txt", $msg, [System.Text.UTF8Encoding]::new($false))
git commit -F "$env:TEMP\git-msg.txt"
Remove-Item "$env:TEMP\git-msg.txt"
```

Use `.NET UTF8Encoding($false)` explicitly — do not rely on default Out-File / redirection encoding; it varies by PS version and adds BOMs that git will write verbatim into the commit message.

#### Accepted glyphs / chars in commit messages

ASCII + CJK characters are both fine. Avoid:
- `→` arrows (U+2192), any kind of decorative Unicode
- leading / trailing whitespace
- literal `"` inside the outer `'...'` block (escape by swapping quote style or use -F)

### Step 5. Sanity check before push

```
git log --oneline -5
git status --porcelain    # MUST be empty after all commits — any leftover M / ?? → abort & diagnose
git status -sb            # confirm branch is `go` and ahead by exactly the number of commits you just made
```

If working tree is not clean after "all done", stop. Common culprits:
- Forgot to `gofmt` a new test file → `??` remains. Stage and add a `style(...)` commit or fold into the already-staged test commit if that commit hasn't been pushed yet.
- File was modified during the add/commit window by auto-save → re-diff and decide whether it belongs.

### Step 6. Push origin go (non-force, always)

```
git push origin go
```

Never use `--force` / `--force-with-lease` against `go` unless the user explicitly types those words in the request. `go` is the shared integration branch; force push silently invalidates CI run references and collaborator checkouts.

After push succeeds, print back the reported push range plus the GitHub Actions link:

- CI: `https://github.com/wabisabi926/faststrm/actions/workflows/ci.yml`
- Release (if a tag went along): `https://github.com/wabisabi926/faststrm/actions/workflows/release.yml`

## Quick recipe for the commonest case: 1 fix + 1 new test file

```
# Step 1 verify
go build ./...
go test -count=1 ./internal/handler/... ./internal/server/... ./internal/service/strm/... ./internal/service/embyproxy/...
# Step 2 format
git status --porcelain
gofmt -w internal/handler/task.go internal/handler/task_bool_validate_test.go
# Step 3 split → group A = runtime fix, group B = new test
# Step 4a commit A
git add internal/handler/task.go
git commit -m 'fix(handler): fillUpsertFromBody parse multipart before FormValue'
# Step 4b commit B
git add internal/handler/task_bool_validate_test.go
git commit -m 'test(handler): UpsertTaskRequest *bool 三态保留语义矩阵覆盖'
# Step 5 clean check
git status --porcelain
git status -sb
# Step 6 push
git push origin go
```

## Output language

All status lines, commit message suggestions, and final push report are in the same language as the user's triggering request. Chinese request ⇒ Chinese report; English request ⇒ English report.
