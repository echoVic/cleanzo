# Clone-aware sizes implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** On macOS, the size of a junk row and the space a clean reports as freed count what removing the files gives back, so blocks shared with APFS clones elsewhere on the disk are no longer counted.

**Architecture:** One darwin function reads a file's APFS clone id and clone count with `getattrlist` (through purego, as `native.go` does). A small cross-platform tally uses it: a file that shares its blocks counts once, and only when every file sharing them was added. The junk scan's candidate walk and the tool-folder walk use the tally; every other size in the app stays as it is.

**Tech Stack:** Go 1.27, `github.com/ebitengine/purego` v0.11.1, `golang.org/x/sys/unix` (tests only, for `Clonefile`). No new dependency.

**Spec:** There is no separate spec. The evidence this plan argues from, measured on the maintainer's Mac (macOS 27.2.1, APFS) on 2026-11-17:

| Folder | Per-file sum (today) | Given back when removed | Overstated |
|---|---|---|---|
| pnpm store (`~/Library/pnpm/store`) | 5.91 GB | 2.72 GB | 3.19 GB (54%) |
| uv cache (`~/.cache/uv`) | 0.60 GB | 0.27 GB | 0.33 GB (54%) |
| `~/Library/Caches`, Go module cache, npm cache | 31.1 GB | 31.1 GB | 0.01 GB |

- A 3 GB file cloned with `cp -c`: `stat` reports 3 GB for each copy, the clone costs 1 MB of free space, and removing the clone frees 0 MB.
- Reading the clone count gives the same totals as reading each file's private size, so the private size is not read.
- Counting per junk candidate loses nothing against counting per rule: the uv cache's entries sum to the whole (0.25 + 0.02 = 0.27 GB).
- Cost: about 3 to 4 µs per file. A 334,026-file store walked in 15.8 to 16.9 s against 14.2 to 15.5 s; a 71,808-file folder in 0.69 s against 0.43 s.

## Global Constraints

- **Scope.** Only the two walks named in Tasks 2 and 3 change. `measureChecked`, `scanPath`, `duSize`, `onDisk` and the growth monitor keep today's numbers.
- **Platforms.** macOS gets the new counting. Windows sizes stay logical: `cloneRefs` there always answers `ok == false`.
- **Fallback.** When macOS or the volume does not report clone data (`ok == false`), a file counts in full, as today. Nothing may error or skip a file because clone data is missing.
- **No new user-visible strings, no change to `JunkItem` or `CleanResult`, no change to `src/`.**
- **Tests never touch the real home.** Use `testEnv`, `t.TempDir`, `fakeTool`, `stageTools`, `writeFile`, `mustMkdir`, `exists`.
- **Test first.** Each task's test is written and seen failing before the code, for the reason the step states.
- **Comments.** Go comments in English, present tense, saying what the code does or why. Never mention a plan, a review, or what the code used to do.
- **Commits.** One commit per task, in the style of `git log` (`fix(junk): …`). No attribution trailers. Never push.
- **Before reporting, run all of:**
  - `test -z "$(gofmt -l .)"`
  - `go vet ./...`
  - `GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go vet ./...`
  - `GOOS=windows GOARCH=arm64 CGO_ENABLED=0 go vet ./...`
  - `go test -race -count=1 -skip '^TestNativeSysctlRawProbes$' ./...` (about three and a half minutes; re-run a timing failure alone before treating it as real)

## File Structure

| File | Change | Responsibility |
|---|---|---|
| `clones_darwin.go` | create | `cloneRefs`: read a file's clone id and clone count from macOS |
| `fs_windows.go` | modify | `cloneRefs` stub next to the `hardLinkID` stub |
| `reclaim.go` | create | `reclaimTally`: sum what removing files gives back |
| `clones_darwin_test.go` | create | `cloneFile` helper, tests for `cloneRefs` and the tally with real clones |
| `reclaim_test.go` | create | the tally with plain files, on every platform |
| `main.go` | modify | `measureReclaimable`, sharing its walk with `measureChecked` |
| `junk.go` | modify | the candidate walk calls `measureReclaimable` |
| `junk_darwin_test.go` | modify | a junk rule sized and cleaned with clones present |
| `tools.go` | modify | `toolRootsSize` adds files to a tally |
| `tools_darwin_test.go` | modify | a tool store sized and cleaned with clones present |

---

### Task 1: Read clone counts and tally what removal gives back

**Files:**
- Create: `clones_darwin.go`
- Create: `reclaim.go`
- Modify: `fs_windows.go:32-33` (add a stub after `hardLinkID`)
- Test: `clones_darwin_test.go` (create), `reclaim_test.go` (create)

**Interfaces:**
- Consumes: `onDisk(info fs.FileInfo) int64` (`fs_darwin.go:27`, `fs_windows.go:30`); test helpers `writeFile(t, path, size)` and `mustMkdir(t, path)` from `main_test.go`.
- Produces:
  - `cloneRefs(path string) (id uint64, refs uint32, ok bool)`
  - `type reclaimTally struct { size int64; shared map[uint64]*cloneFamily }`, zero value ready to use
  - `func (t *reclaimTally) add(path string, info fs.FileInfo)`; read the total from `t.size`
  - test helper `cloneFile(t *testing.T, src, dst string)` in `clones_darwin_test.go`, used by Tasks 2 and 3

- [x] **Step 1: Write the failing tests**

Create `clones_darwin_test.go`:

```go
package main

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

// cloneFile makes dst an APFS clone of src, sharing its blocks. The test is
// skipped on a volume that cannot clone.
func cloneFile(t *testing.T, src, dst string) {
	t.Helper()
	mustMkdir(t, filepath.Dir(dst))
	if err := unix.Clonefile(src, dst, unix.CLONE_NOFOLLOW); err != nil {
		t.Skipf("this volume cannot clone files: %v", err)
	}
}

// tallyOf adds the given files to one tally and returns its size.
func tallyOf(t *testing.T, paths ...string) int64 {
	t.Helper()
	var sum reclaimTally
	for _, p := range paths {
		info, err := os.Lstat(p)
		if err != nil {
			t.Fatal(err)
		}
		sum.add(p, info)
	}
	return sum.size
}

// TestCloneRefs reads the clone count macOS keeps for a file.
func TestCloneRefs(t *testing.T) {
	dir := t.TempDir()
	a, b, plain := filepath.Join(dir, "a"), filepath.Join(dir, "b"), filepath.Join(dir, "plain")
	writeFile(t, a, 2000)
	writeFile(t, plain, 2000)
	if _, refs, ok := cloneRefs(plain); !ok || refs != 1 {
		t.Fatalf("plain: refs = %d, ok = %v", refs, ok)
	}
	cloneFile(t, a, b)
	ida, ra, oka := cloneRefs(a)
	idb, rb, okb := cloneRefs(b)
	if !oka || !okb || ra != 2 || rb != 2 || ida == 0 || ida != idb {
		t.Fatalf("a = %d/%d/%v, b = %d/%d/%v", ida, ra, oka, idb, rb, okb)
	}
	if idp, _, _ := cloneRefs(plain); idp == ida {
		t.Fatalf("plain shares clone id %d", idp)
	}
	if _, _, ok := cloneRefs(filepath.Join(dir, "missing")); ok {
		t.Fatal("a missing file reported clone data")
	}
	if err := os.Remove(b); err != nil {
		t.Fatal(err)
	}
	if _, refs, ok := cloneRefs(a); !ok || refs != 1 {
		t.Fatalf("after removing the clone: refs = %d, ok = %v", refs, ok)
	}
}

// TestReclaimTallyCountsClonesOnce counts shared blocks once, and only when
// every file sharing them is in the tally.
func TestReclaimTallyCountsClonesOnce(t *testing.T) {
	dir := t.TempDir()
	kept := filepath.Join(dir, "kept")
	b, c, plain := filepath.Join(dir, "in/b"), filepath.Join(dir, "in/c"), filepath.Join(dir, "in/plain")
	writeFile(t, kept, 3000)
	writeFile(t, plain, 700)
	cloneFile(t, kept, b)
	cloneFile(t, kept, c)
	if got := tallyOf(t, b, c, plain); got != 700 {
		t.Fatalf("clones of a file kept elsewhere = %d, want 700", got)
	}
	if got := tallyOf(t, kept, b, c, plain); got != 3700 {
		t.Fatalf("the whole family = %d, want 3700", got)
	}
}
```

Create `reclaim_test.go`:

```go
package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestReclaimTallySumsPlainFiles counts files that share nothing in full.
func TestReclaimTallySumsPlainFiles(t *testing.T) {
	dir := t.TempDir()
	var sum reclaimTally
	for name, size := range map[string]int{"a": 700, "b": 1300, "empty": 0} {
		p := filepath.Join(dir, name)
		writeFile(t, p, size)
		info, err := os.Lstat(p)
		if err != nil {
			t.Fatal(err)
		}
		sum.add(p, info)
	}
	if sum.size != 2000 {
		t.Fatalf("size = %d, want 2000", sum.size)
	}
}
```

- [x] **Step 2: Run the tests to verify they fail**

Run: `go vet .`
Expected: FAIL with `undefined: reclaimTally` (and `undefined: cloneRefs`).

- [x] **Step 3: Write `clones_darwin.go`**

```go
package main

import (
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

// Constants from <sys/attr.h>.
const (
	attrBitMapCount       = 5          // ATTR_BIT_MAP_COUNT
	attrCmnReturnedAttrs  = 0x80000000 // ATTR_CMN_RETURNED_ATTRS
	attrCmnextCloneID     = 0x00000100 // ATTR_CMNEXT_CLONEID
	attrCmnextCloneRefcnt = 0x00001000 // ATTR_CMNEXT_CLONE_REFCNT
	fsoptNoFollow         = 0x00000001 // FSOPT_NOFOLLOW
	fsoptPackInvalAttrs   = 0x00000008 // FSOPT_PACK_INVAL_ATTRS
	fsoptAttrCmnExtended  = 0x00000020 // FSOPT_ATTR_CMN_EXTENDED
)

// attrList mirrors struct attrlist. With FSOPT_ATTR_CMN_EXTENDED its last
// field carries the ATTR_CMNEXT_* bits.
type attrList struct {
	bitmapCount, reserved                   uint16
	common, volume, directory, file, cmnExt uint32
}

// cloneReply is what getattrlist writes for the attributes cloneRefs asks
// for, in the order macOS packs them: the length, the attributes it
// returned, the clone id and the clone count.
type cloneReply struct {
	length   uint32
	returned [5]uint32 // attribute_set_t; the last word holds the ATTR_CMNEXT_* bits
	cloneID  uint64
	refs     uint32
}

var (
	getattrlistOnce sync.Once
	getattrlistFn   func(path string, list *attrList, buf unsafe.Pointer, size uintptr, options uint32) int32
)

// cloneRefs reads how many files share a file's blocks as APFS clones (1
// for a file that shares none) and the id those files have in common. ok is
// false when macOS or the volume does not say, as on a volume that is not
// APFS.
func cloneRefs(path string) (id uint64, refs uint32, ok bool) {
	getattrlistOnce.Do(func() {
		lib, err := purego.Dlopen("/usr/lib/libSystem.B.dylib", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			return
		}
		if _, err := purego.Dlsym(lib, "getattrlist"); err == nil {
			purego.RegisterLibFunc(&getattrlistFn, lib, "getattrlist")
		}
	})
	if getattrlistFn == nil {
		return 0, 0, false
	}
	const wanted = attrCmnextCloneID | attrCmnextCloneRefcnt
	list := attrList{bitmapCount: attrBitMapCount, common: attrCmnReturnedAttrs, cmnExt: wanted}
	var reply cloneReply
	if getattrlistFn(path, &list, unsafe.Pointer(&reply), unsafe.Sizeof(reply), fsoptNoFollow|fsoptPackInvalAttrs|fsoptAttrCmnExtended) != 0 {
		return 0, 0, false
	}
	if reply.returned[4]&wanted != wanted {
		return 0, 0, false
	}
	return reply.cloneID, reply.refs, true
}
```

Why this shape:
- `FSOPT_PACK_INVAL_ATTRS` with `ATTR_CMN_RETURNED_ATTRS` makes macOS always write both fields at fixed offsets and say in `returned` which ones are real. That is what the `returned[4]&wanted` check reads.
- `cloneID` lands at offset 24 and `refs` at 32, which is where Go's natural alignment puts them. Do not reorder the fields.
- A macOS that does not know `ATTR_CMNEXT_CLONE_REFCNT` fails the call or leaves the bit out of `returned`; both give `ok == false`.

- [x] **Step 4: Write `reclaim.go`**

```go
package main

import "io/fs"

// reclaimTally sums what removing the files added to it gives back. Files
// that are APFS clones of each other share their blocks, and those blocks
// are freed only when the last of them goes: they count once, and only when
// every file sharing them was added.
type reclaimTally struct {
	size   int64
	shared map[uint64]*cloneFamily
}

// cloneFamily is the files of one clone id added so far.
type cloneFamily struct {
	seen uint32
	size int64
}

// add counts one regular file.
func (t *reclaimTally) add(path string, info fs.FileInfo) {
	size := onDisk(info)
	if size == 0 {
		return
	}
	id, refs, ok := cloneRefs(path)
	if !ok || refs <= 1 {
		t.size += size
		return
	}
	if t.shared == nil {
		t.shared = map[uint64]*cloneFamily{}
	}
	family := t.shared[id]
	if family == nil {
		family = &cloneFamily{}
		t.shared[id] = family
	}
	family.seen++
	family.size = max(family.size, size)
	if family.seen == refs {
		t.size += family.size
	}
}
```

- [x] **Step 5: Add the Windows stub**

In `fs_windows.go`, directly after

```go
// hardLinkID is never set on Windows, where sizes stay logical.
func hardLinkID(fs.FileInfo) (uint64, bool) { return 0, false }
```

add

```go

// cloneRefs is never set on Windows, where sizes stay logical.
func cloneRefs(string) (id uint64, refs uint32, ok bool) { return 0, 0, false }
```

- [x] **Step 6: Run the tests to verify they pass**

Run: `go test -race -count=1 -run 'TestCloneRefs|TestReclaimTally' -v .`
Expected: PASS for `TestCloneRefs`, `TestReclaimTallyCountsClonesOnce`, `TestReclaimTallySumsPlainFiles`. A SKIP of the first two means the temp folder is not on APFS; report it, do not weaken the test.

Run: `test -z "$(gofmt -l .)" && go vet . && GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go vet . && GOOS=windows GOARCH=arm64 CGO_ENABLED=0 go vet .`
Expected: no output.

- [x] **Step 7: Commit**

```bash
git add clones_darwin.go clones_darwin_test.go reclaim.go reclaim_test.go fs_windows.go
git commit -m "feat(junk): read APFS clone counts and tally what removal gives back"
```

---

### Task 2: Junk rows count what removing them gives back

**Files:**
- Modify: `main.go:196-230` (`measureChecked`)
- Modify: `junk.go:807`
- Test: `junk_darwin_test.go` (append)

**Interfaces:**
- Consumes: `reclaimTally`, `(*reclaimTally).add`, test helper `cloneFile` (Task 1); `testEnv`, `scanJunk`, `cleanJunk`, `JunkSelection`, `cleanHooks`, `writeFile`, `exists`.
- Produces: `measureReclaimable(p string, contexts ...context.Context) (size, files int64, newest time.Time, readErrors int)`, same contract as `measureChecked`.

- [x] **Step 1: Write the failing test**

Append to `junk_darwin_test.go`:

```go

// TestJunkSizesCountClonesOnce sizes a cache by what removing it gives
// back: a file cloned into a project frees nothing, and two clones that
// both sit in the cache free their blocks once.
func TestJunkSizesCountClonesOnce(t *testing.T) {
	env := testEnv(t)
	cache := filepath.Join(env.home, ".cache/uv/archive-v0")
	shared, inProject := filepath.Join(cache, "pkg/lib.so"), filepath.Join(env.home, "proj/.venv/lib.so")
	writeFile(t, shared, 3000)
	cloneFile(t, shared, inProject)
	writeFile(t, filepath.Join(cache, "pkg/first.bin"), 2000)
	cloneFile(t, filepath.Join(cache, "pkg/first.bin"), filepath.Join(cache, "pkg/second.bin"))
	writeFile(t, filepath.Join(cache, "pkg/meta"), 700)

	items, _ := scanJunk(context.Background(), env, nil, nil)
	if len(items) != 1 || items[0].ID != "dev.uv" || items[0].Size != 2700 {
		t.Fatalf("items = %+v, want dev.uv at 2700", items)
	}
	res := cleanJunk(context.Background(), env, []JunkSelection{{ID: "dev.uv"}}, time.Time{}, cleanHooks{})
	if len(res) != 1 || res[0].Freed != 2700 || exists(cache) || !exists(inProject) {
		t.Fatalf("result = %+v", res)
	}
}
```

2700 is `first.bin` and `second.bin` once (2000) plus `meta` (700); `lib.so` (3000) stays alive in the project.

- [x] **Step 2: Run the test to verify it fails**

Run: `go test -race -count=1 -run TestJunkSizesCountClonesOnce .`
Expected: FAIL with `items = [{ID:dev.uv … Size:7700 …}], want dev.uv at 2700`.

- [x] **Step 3: Split the walk in `main.go`**

Replace the head of `measureChecked`

```go
// measureChecked preserves partial byte counts and reports every traversal
// failure. The walk stops once the optional context is canceled.
func measureChecked(p string, contexts ...context.Context) (size, files int64, newest time.Time, readErrors int) {
	if runtime.GOOS == "windows" && !platformCleanPath(p) {
		return 0, 0, time.Time{}, 1
	}
```

with

```go
// measureChecked preserves partial byte counts and reports every traversal
// failure. The walk stops once the optional context is canceled.
func measureChecked(p string, contexts ...context.Context) (size, files int64, newest time.Time, readErrors int) {
	return measureTree(p, false, contexts...)
}

// measureReclaimable is measureChecked for something about to be removed:
// blocks that APFS clones share count once, and only when every file
// sharing them is under p (see reclaimTally).
func measureReclaimable(p string, contexts ...context.Context) (size, files int64, newest time.Time, readErrors int) {
	return measureTree(p, true, contexts...)
}

// measureTree walks p for measureChecked and measureReclaimable.
func measureTree(p string, reclaim bool, contexts ...context.Context) (size, files int64, newest time.Time, readErrors int) {
	if runtime.GOOS == "windows" && !platformCleanPath(p) {
		return 0, 0, time.Time{}, 1
	}
	var tally reclaimTally
```

and the tail of the same function

```go
		if d.Type().IsRegular() {
			size += onDisk(info)
			files++
		}
		return nil
	})
	return size, files, newest, readErrors
}
```

with

```go
		if d.Type().IsRegular() {
			if reclaim {
				tally.add(path, info)
			} else {
				size += onDisk(info)
			}
			files++
		}
		return nil
	})
	return size + tally.size, files, newest, readErrors
}
```

The body between the two (context check, error counting, `platformMeasureEntry`, `changedAt`) stays untouched.

- [x] **Step 4: Use it for junk candidates**

In `junk.go` line 807, inside the candidate goroutine, change

```go
				size, files, changed, failures = measureChecked(c.path, ctx)
```

to

```go
				size, files, changed, failures = measureReclaimable(c.path, ctx)
```

Leave the `measureCrashpad` branch above it and every other caller of `measureChecked` alone.

- [x] **Step 5: Run the tests to verify they pass**

Run: `go test -race -count=1 -run 'TestJunkSizesCountClonesOnce|TestExpandShowsOnlyWhatExists|TestNestedJunkItemsCountOnce' .`
Expected: `ok  	cleanzo`.

- [x] **Step 6: Commit**

```bash
git add main.go junk.go junk_darwin_test.go
git commit -m "fix(junk): size rows by what removing them frees when files are APFS clones"
```

---

### Task 3: Tool stores count what their cleanup gives back

**Files:**
- Modify: `tools.go:230-281` (`toolRootsSize`)
- Test: `tools_darwin_test.go` (append)

**Interfaces:**
- Consumes: `reclaimTally`, `(*reclaimTally).add`, test helper `cloneFile` (Task 1); `testEnv`, `fakeTool`, `stageTools`, `shellPath`, `scanJunk`, `cleanJunk`.
- Produces: nothing new. `toolRootsSize(roots []string) (int64, error)` keeps its signature; `cleanTool` keeps computing `res.Freed = max(0, before-after)` from it.

- [x] **Step 1: Write the failing test**

Append to `tools_darwin_test.go`:

```go

// TestToolRootsCountClonesOnce sizes a tool's store by what its cleanup
// gives back: a file cloned into a project frees nothing when the store's
// copy goes.
func TestToolRootsCountClonesOnce(t *testing.T) {
	env := testEnv(t)
	bin := t.TempDir()
	root := filepath.Join(env.home, "tool-store")
	shared, own := filepath.Join(root, "shared"), filepath.Join(root, "own")
	inProject := filepath.Join(env.home, "proj/node_modules/shared")
	writeFile(t, shared, 3000)
	writeFile(t, own, 700)
	cloneFile(t, shared, inProject)
	fakeTool(t, bin, "pnpm", `case "$*" in "store path") echo `+shellPath(root)+`;; "store prune") rm -f `+shellPath(shared)+` `+shellPath(own)+`;; *) exit 1;; esac`)
	stageTools(t, &env, bin)

	items, _ := scanJunk(context.Background(), env, nil, nil)
	if len(items) != 1 || items[0].ID != "dev.pnpm-store" || items[0].Size != 700 || !items[0].Estimate {
		t.Fatalf("items = %+v, want dev.pnpm-store at 700", items)
	}
	res := cleanJunk(context.Background(), env, []JunkSelection{{ID: "dev.pnpm-store"}}, time.Time{}, cleanHooks{})
	if len(res) != 1 || res[0].Freed != 700 || exists(own) || !exists(inProject) {
		t.Fatalf("result = %+v", res)
	}
}
```

- [x] **Step 2: Run the test to verify it fails**

Run: `go test -race -count=1 -run TestToolRootsCountClonesOnce .`
Expected: FAIL with `items = [{ID:dev.pnpm-store … Size:3700 …}], want dev.pnpm-store at 700`.

- [x] **Step 3: Tally the tool roots**

In `tools.go`, change the head of `toolRootsSize`

```go
// toolRootsSize validates roots and measures without following links or ignoring cancellation.
func (env junkEnv) toolRootsSize(roots []string) (int64, error) {
	var size int64
```

to

```go
// toolRootsSize validates roots and measures without following links or
// ignoring cancellation. It counts what removing the roots gives back:
// blocks that APFS clones share count once, and only when every file
// sharing them is under the roots (see reclaimTally).
func (env junkEnv) toolRootsSize(roots []string) (int64, error) {
	var tally reclaimTally
```

and its tail

```go
			if info.Mode().IsRegular() {
				size += onDisk(info)
			}
			return nil
		})
		if err != nil {
			return 0, err
		}
	}
	return size, nil
}
```

to

```go
			if info.Mode().IsRegular() {
				tally.add(path, info)
			}
			return nil
		})
		if err != nil {
			return 0, err
		}
	}
	return tally.size, nil
}
```

One tally covers all roots of a command, so a clone family spread over two roots still counts once. The guards before the walk stay untouched.

- [x] **Step 4: Run the tests to verify they pass**

Run: `go test -race -count=1 -run 'TestToolRootsCountClonesOnce|TestToolRootsAreMeasured|TestToolEstimateSkipsLinksAndSpecialFiles|TestSimulatorCleanupUsesDeviceRoots' .`
Expected: `ok  	cleanzo`.

- [x] **Step 5: Run the full checks from Global Constraints**

Expected: every command exits 0; the full suite prints `ok  	cleanzo` (about 195 s).

- [x] **Step 6: Commit**

```bash
git add tools.go tools_darwin_test.go
git commit -m "fix(junk): report what a tool's cleanup frees when its store holds APFS clones"
```

---

## Validation ledger (2026-11-17)

Branch `fix/clone-aware-sizes`, three task commits on top of the plan. All runs on the maintainer's Mac (macOS 27.2.1), read-only; no real clean was run.

- `test -z "$(gofmt -l .)"`, `go vet ./...` and both Windows `go vet` runs: clean.
- `go test -race -count=1 ./...` with the CI skip: `ok  	cleanzo	202.136s`.
- `measureChecked` against `measureReclaimable` on the real folders: pnpm store 5.91 → 2.72 GB, `~/.cache/uv/archive-v0` 0.58 → 0.25 GB, 0 read errors.
- One real junk scan through `currentJunkEnv()`, built from `master` and from the branch, 121 rows each:
  - `dev.uv` found size 598.7 → 272.8 MB (`archive-v0` 580.4 → 254.5 MB). Its selected size is 18.3 MB in both, because `archive-v0` is in use by a running `uv tool` process.
  - `app:com.tencent.inputmethod.wetype` 28.4 → 17.9 MB. Every other row differs by under 0.2 MB, which is files changing between runs.
  - `dev.pnpm-store` is 0 in both: `pnpm store path` fails on this Mac for both installed launchers (a Node module loader error), so the row is hidden with or without this change. The 3.19 GB the store overstates is therefore not visible in the app here until pnpm works again.
  - Scan time: 21.8, 14.0 and 14.1 s from `master`; 13.4, 15.1 and 14.6 s from the branch. No measurable difference.
- Not done: opening the built app and reading the rows on screen. The scan above runs the same code the page calls.

## Not in this plan

- Hard links: a file with several names still counts once per name it is found under. The measured stores hold none (0.00 GB); `~/Library/Caches` holds 0.03 GB.
- A line in the row that says how much is shared with projects and stays ("另有 3.2 GB 与项目共用"). It needs a new `JunkItem` field, strings in both languages and page tests.
- Sizes outside the junk scan: Space analysis, Large files, Apps, AI space, the growth monitor.
- Files macOS marks purgeable.
- The CHANGELOG entry, which is written with the release.
