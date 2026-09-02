# Homesink — Execution Runbook

How to actually get this built. `08-ROADMAP.md` says *what* order; this says *what you do*.

---

## 1. Where the project actually stands

| | Status |
|---|---|
| **M0 — contract frozen** | ✅ **Done and green.** 16 paths, 18 operations, 41 fixtures, all validating. |
| Repo | `github.com/FancyFunction/homesink`, branch `main`, clean tree |
| Everything else | Not started. No backend Go module, no client Gradle project. |

Verify M0 for yourself any time:

```bash
cd /local-development/homesink/contract && go test ./... && go run ./cmd/validate -spec ../docs/api/openapi.yaml -fixtures ../backend/internal/testutil/testdata
```

**You start at WP-B1 and WP-C1.** They have no dependencies on each other and can run in parallel.

---

## 2. Settle this before WP-B1

**The Go module path does not match the git remote.**

```
go module : github.com/christiankruse/homesink/contract
git remote: github.com/FancyFunction/homesink
```

`WP-B1` is about to create a second module (`…/homesink/backend`) and `07-DEPLOYMENT.md` derives the
image path `ghcr.io/<owner>/homesink` from the same name. Fix it now while one module exists and the
change is three lines; fix it later and it touches every import in the repo.

**Recommended:** rename to match the remote.

```bash
cd /local-development/homesink/contract
sed -i 's|github.com/christiankruse/homesink|github.com/FancyFunction/homesink|' go.mod
grep -rl 'christiankruse/homesink' . | xargs -r sed -i 's|christiankruse/homesink|FancyFunction/homesink|g'
go mod tidy && go test ./...
```

Then use `github.com/FancyFunction/homesink/backend` in WP-B1 and `ghcr.io/fancyfunction/homesink`
(registry paths are lowercase) in WP-B12. If you would rather keep `christiankruse`, that is fine —
just make the remote, both modules and the registry agree, and say which in WP-B1's prompt.

---

## 3. One-time machine setup

Present already: Go 1.25.5 ✅ · JDK 17 ✅ · ffmpeg/ffprobe 6.1.1 ✅ · adb ✅ · git ✅ · Android SDK
(platforms 36 + 37, build-tools 36/37, cmdline-tools) ✅ · 428 GB free ✅

### 3.1 Needed for the backend packages

```bash
go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest
```
`golangci-lint` is in every backend package's definition of done. Add Go's bin dir to your PATH if it
isn't already — put this in `~/.bashrc`:
```bash
export PATH="$PATH:$(go env GOPATH)/bin"
```

### 3.2 Needed for the client packages

Gradle is required **once**, to generate the wrapper. After that `./gradlew` is committed and you
never need a system Gradle again. AGP 8.7+ needs Gradle 8.9+, so check the version you get:

```bash
sudo apt install gradle && gradle --version
```
If that gives you anything below 8.9, use SDKMAN instead:
```bash
curl -s https://get.sdkman.io | bash && source ~/.sdkman/bin/sdkman-init.sh && sdk install gradle 8.14
```

Export the SDK locations — Gradle will not find them otherwise (both are currently unset):
```bash
export ANDROID_HOME="$HOME/Android/Sdk"
export JAVA_HOME="$(dirname "$(dirname "$(readlink -f "$(which javac)")")")"
```
Put both in `~/.bashrc`. Alternatively WP-C1 can write `client/local.properties` with
`sdk.dir=/home/christian/Android/Sdk` — that file is per-machine and must stay out of git.

### 3.3 Needed only from WP-B12 / milestone M5

```bash
sudo apt install podman && podman --version
```
Quadlets need **podman ≥ 4.4**. Ubuntu 24.04 ships 4.9 ✅. **Linux Mint 21 ships podman 3.4, which is
too old** — if your sink box is Mint 21 you need Mint 22 or a newer podman source. Worth checking now
rather than at M5.

### 3.4 Optional but useful

```bash
sudo apt install jq
```

---

## 4. Hardware, and when you need it

| What | Needed by | Why |
|---|---|---|
| **An Android phone**, USB debugging on | WP-C3, and *urgently* for WP-C10 | No emulator is installed, and the deletion-consent flow (D-22) behaves differently on real hardware. The risk register says prototype C10 on a real device during M1 — that means you need the phone early, not at M3. |
| A phone with a **real photo library** | WP-C7, WP-C8 | A handful of test images will not surface the scanner and paging behaviour that 20 000 items will. |
| **The sink box** (Mint/Ubuntu + the drive) | M5 | Not needed before then; develop against a locally-run `homesinkd`. |
| A **release keystore** | WP-C15 | Create it early and back it up in two places. Losing it means every user must uninstall and reinstall (D-34). |
| **GHCR access** on the GitHub account | WP-B12 | Decide public vs private image — a private one needs a pull secret in the quadlet (`07 §7`). |

Connect the phone and confirm:
```bash
adb devices
```

---

## 5. How to run one work package

This is the core loop. **One package per session, fresh context.** The packages were written so an
implementer needs the decision register, its own section, and its dependencies — nothing else. Giving
a session the whole repo to read defeats that and produces worse results.

### 5.1 The dispatch prompt

Paste this, substituting the package id and its doc:

```
Implement WP-B2 from docs/04-WORKPACKAGES-BACKEND.md in /local-development/homesink.

Read first, in this order:
  docs/00-ARCHITECTURE.md
  docs/01-DECISIONS.md
  docs/03-DATA-MODEL.md  (section 1)
  docs/04-WORKPACKAGES-BACKEND.md  — the WP-B2 section ONLY

Rules:
- Create only the files listed under "Creates" in WP-B2.
- Do not edit any file owned by another work package. The ownership table is docs/08-ROADMAP.md section 4.
- internal/core is frozen: read it, never modify it.
- Every acceptance criterion in WP-B2 must have its own named test.
- If you need a decision that is not written down, STOP and ask. Do not invent one.

Done when all of these pass, from the backend/ directory:
  go build ./... && go vet ./... && golangci-lint run && go test ./... -race

Finish by listing each WP-B2 acceptance criterion next to the test name that covers it.
```

Swap in `docs/05-WORKPACKAGES-CLIENT.md` and the Gradle commands for client packages:
```
  ./gradlew :app:assembleDebug :app:lintDebug :app:testDebugUnitTest
```

### 5.2 The acceptance gate

Do not merge a package until all five hold. This is the whole reason the plan is shaped this way —
skipping the gate is how a bad package silently poisons the ones that depend on it.

1. **The commands pass.** Not "should pass" — you ran them.
2. **Every acceptance criterion maps to a named test.** The session lists them; spot-check two.
3. **No files outside its "Creates" list changed.** `git diff --stat` against the branch point.
4. **Frozen packages untouched.** `git diff -- backend/internal/core client/.../contract` is empty.
5. **No new decision was invented.** If the session made a judgement call, it belongs in
   `01-DECISIONS.md` *first*, with a D-number, then in the code.

Check 3 and 4 quickly:
```bash
git diff --stat main...HEAD
git diff main...HEAD -- backend/internal/core client/app/src/main/java/de/homesink/app/contract
```

### 5.3 Match the model to the tier

| Tier | Packages | How to run them |
|---|---|---|
| **S** | B1 B9 B10 B13 B14 · C1 C2 C4 C12 C15 | Hand off with light supervision. Spot-check the gate. |
| **M** | B2 B3 B5 B6 B7 B11 B12 · C3 C5 C6 C7 C9 C13 C14 | Normal supervision. Read the diff. |
| **L** | **B4 B8 · C8 C10 C11** | Strongest model available, and read the code yourself. These five are where the correctness of the whole system lives. |

### 5.4 Running packages in parallel

The ownership table makes parallel work *safe in principle*, but two sessions editing one working tree
will still collide. Use a worktree per package:

```bash
git worktree add ../homesink-B5 -b wp-b5
# run the session with cwd=/local-development/homesink-B5
# when the gate passes:
git worktree remove ../homesink-B5
```
Practical ceiling is 2–3 concurrent packages. More than that and reviewing the output becomes the
bottleneck, which defeats the point.

---

## 6. The schedule

### Wave 1 — start here, both in parallel
```
WP-B1  skeleton, config, lifecycle, frozen internal/core
WP-C1  Gradle scaffold, theme, navigation, frozen contract package
```
These two create the frozen packages everything else imports. **Review them properly** — a mistake in
`core`/`contract` propagates into all 27 remaining packages. Once merged, do not edit them.

### Wave 2
```
WP-B2  store          (blocks most of the backend)
WP-C2  Room  ·  WP-C5 network   (parallel)
```

### Wave 3 — wide parallelism
```
backend: B3, B5, B6, B9, B11, B13
client:  C3, C4, C6, C13, C14
```

### Wave 4
```
backend: B4, B7  →  B8  →  B10  →  B12, B14
client:  C7, C8  →  C9, C10        ·  C11 → C12 (parallel to all of it)
```

`WP-C15` last. Milestone boundaries and what each proves are in `08-ROADMAP.md §2`.

---

## 7. Checkpoints — verify milestones for real, not by ticking boxes

**After M1 (B1 B2 B3 C1 C2 C5 C6):** run the server locally and pair a real phone with it.

```bash
cd backend && go run ./cmd/homesinkd            # HOMESINK_DATA=/tmp/homesink-dev
./homesinkd pair                                 # prints the 6-digit code
cd ../client && ./gradlew installDebug           # phone attached via adb
```
Success = the phone shows the paired server and survives an app restart. This proves pairing, TLS
pinning and mDNS — the integration most likely to surprise you.

**During M1, in parallel: prototype WP-C10 on the real device.** Not the full package — just prove that
`MediaStore.createDeleteRequest` with 50 URIs produces one dialog and actually deletes. If it doesn't
behave as D-22 expects, you want to know now, while only two client packages depend on it.

**After M2:** sync ~50 real photos including one duplicate and one >30 MB video. Then check the drive
directly — `find /tmp/homesink-dev/library -type f` should read as `Album/YYYY/MM/file`. Kill the app
mid-upload and confirm it resumes rather than restarting.

**After M4:** let it run for a week on your own phone. The scheduler is the one thing you cannot
verify in an afternoon.

**M5:** follow `07-DEPLOYMENT.md §4`. Test the rollback deliberately by publishing an image whose
entrypoint exits 1, and confirm podman restores the previous one.

---

## 8. Only you can do these

Everything else can be delegated. These cannot:

- **Generate and back up the release keystore** (before WP-C15). Two locations, neither of them only
  this laptop.
- **Pair a real phone** and grant the media permissions.
- **Decide the five open questions** in `01 §13` and `07 §7`. Four have defaults that ship silently;
  the GHCR path does not and blocks M5.
- **Judge whether transcoded video looks good enough.** D-29 picks CRF 24 as a starting point. Watch a
  transcoded family video on a real screen at M4 and adjust — no test asserts "looks fine".

---

## 9. When a package goes wrong

| Symptom | What it means | Do this |
|---|---|---|
| Session asks for a decision that isn't documented | A real gap in the plan | Add it to `01-DECISIONS.md` with a D-number, then re-run the package. Don't answer inline — the next session will ask again. |
| Diff touches files outside its "Creates" list | Boundary violation | Reject. Re-run pointing at `08 §4`. Usually means the package genuinely needs something it doesn't own — that's a plan bug worth fixing. |
| Acceptance criteria "covered" by one broad test | The gate was gamed | Reject. The criteria are deliberately specific; one test per criterion. |
| Package depends on something not yet built | Wrong wave | Check the graph in `08 §1`. Build against the fake (`testutil.MemStore`, MockWebServer) rather than reordering. |
| Contract lane fails after an unrelated change | Someone edited `openapi.yaml` | That file is WP-0-owned. A change there is a contract change and must update fixtures on both sides. |
