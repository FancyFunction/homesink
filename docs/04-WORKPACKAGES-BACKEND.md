# Backend Work Packages (Go)

**How to use this file.** Each `WP-B*` is a self-contained assignment. To implement one you need: this
section, `01-DECISIONS.md`, and the files listed under *Depends on*. You do **not** need to read other
work packages, and you must **not** edit files owned by another package (`08-ROADMAP.md §4`).

**Tier** = expected difficulty. `S` = mechanical, follow the spec. `M` = needs judgement inside one file.
`L` = genuinely tricky, assign to a stronger model or review closely.

**Definition of done, for every package:** code compiles, `go vet ./...` clean, `golangci-lint` clean,
unit tests pass, the listed acceptance criteria have a named test each, and no `TODO` remains.

Module path: `github.com/christiankruse/homesink/backend` (adjust once open question ❓4 is answered).

---

## WP-B1 — Skeleton, config, logging, lifecycle · Tier S

**Goal.** A daemon that starts, reads config, serves `/healthz`, and shuts down cleanly. No features.

**Creates**
```
cmd/homesinkd/main.go
internal/config/config.go        internal/config/config_test.go
internal/core/types.go           ← FROZEN after this WP
internal/core/errors.go          ← FROZEN after this WP
internal/httpapi/server.go       internal/httpapi/middleware.go
internal/buildinfo/buildinfo.go  (version, injected via -ldflags)
```

**`internal/core` — the frozen contract.** Every other package imports this and nothing imports back.
```go
package core

type MediaType string
const (MediaImage MediaType = "image"; MediaVideo MediaType = "video"; MediaAudio MediaType = "audio")

type Blob struct { Hash string; SizeBytes int64; MimeType string; MediaType MediaType
                   RelPath string; StoredHash string; OriginalReplacedAt *int64
                   Width, Height int; DurationMs int64; CreatedAt int64 }

type Item struct { ItemID, Hash, Album string; Year, Month int
                   Filename, RelPath string; CapturedAtMs int64
                   CapturedOffsetMin int; DeviceID string; CreatedAt int64 }

type Placement struct { Album string; CapturedAtMs int64; CapturedOffsetMin int
                        Filename string }

// Store is the whole persistence surface. Implemented by store.SQLite, faked by testutil.MemStore.
type Store interface {
    BlobByHash(ctx context.Context, hash string) (*Blob, error)
    InsertBlob(ctx context.Context, b Blob) error
    InsertItem(ctx context.Context, it Item) error
    ItemByPath(ctx context.Context, relPath string) (*Item, error)
    // …extended by WP-B2; each later WP appends its own methods and its own fake methods.
}

type Error struct { Code string; Message string; Retryable bool; HTTPStatus int; Details map[string]any }
func (e *Error) Error() string
// Constructors for every code in 02-API.md §2 — e.g. core.ErrHashMismatch(expected, actual string) *Error
```

**Config** — env only, no config file (D: minimal configuration is a requirement).
| Var | Default | Notes |
|---|---|---|
| `HOMESINK_DATA` | `/data` | must exist and be writable; fail fast otherwise |
| `HOMESINK_ADDR` | `:8443` | |
| `HOMESINK_TLS` | `on` | `off` → h2c (D-02) |
| `HOMESINK_SERVER_NAME` | hostname | shown during pairing |
| `HOMESINK_MAX_FILE_BYTES` | `17179869184` | 16 GiB (D-21) |
| `HOMESINK_VIDEO_CODEC` | `hevc` | `hevc`\|`h264` (D-29) |
| `HOMESINK_WORKERS` | `0` | 0 → `max(1, NumCPU/2)` (D-32) |
| `HOMESINK_KEEP_ORIGINAL_VIDEOS` | `false` | `true` disables the D-30 replacement |
| `HOMESINK_UPDATE_CHECK` | `on` | D-35 |
| `HOMESINK_LOG_LEVEL` | `info` | |
| `TZ` | `Europe/Berlin` | nightly jobs and logs only (❓5) |

**Also.** `log/slog` JSON to stdout; middleware for request-id, panic recovery, access log, and
`X-Homesink-Time`. `SIGTERM` → stop accepting, drain in-flight ≤30 s, close DB.

**Acceptance.** Missing/unwritable `HOMESINK_DATA` exits non-zero with a readable message · every env var
has a table-driven parse test incl. the invalid case · `/healthz` returns 200 before any other subsystem
is ready · `SIGTERM` during an in-flight request completes it.

---

## WP-B2 — Persistence layer · Tier M

**Goal.** `store.SQLite` implementing `core.Store`, plus migrations and backups.
**Depends on** WP-B1.

**Creates** `internal/store/{sqlite.go,migrate.go,blobs.go,items.go,jobs.go,devices.go,stats.go,backup.go}`
+ tests, `migrations/0001_init.sql` (verbatim from `03-DATA-MODEL.md §1.1`),
`internal/testutil/memstore.go` (in-memory fake used by every other package's tests).

**Rules.**
- Open with the four pragmas from `03-DATA-MODEL.md §1`. Pure-Go driver `modernc.org/sqlite` — adding a
  cgo driver breaks the container build.
- **One writer.** All writes go through a single `*sql.DB` with `SetMaxOpenConns(1)` for the write handle
  and a separate read-only handle pool. This is the standard SQLite-under-concurrency shape and removes
  almost every `SQLITE_BUSY`.
- `album_stats` is updated in the *same transaction* as the `items` write (invariant S3).
- Migrations: embedded, forward-only, transactional, recorded. Re-read the warning in `03 §1.3`.
- `Backup()` = `VACUUM INTO`, nightly + pre-migration, keep 7 (D-36).

**Acceptance.** Migration from empty DB and re-run are both no-ops the second time · `album_stats`
matches a re-derived aggregate after 1000 random insert/delete ops · concurrent 8-goroutine
insert+read test passes with `-race` · `MemStore` and `SQLite` pass the **same** shared test suite
(`store/conformance_test.go` — write it once, run it against both).

---

## WP-B3 — Pairing, tokens, TLS · Tier M

**Goal.** Device enrolment and request authentication. Implements D-01, D-02.
**Depends on** WP-B1, WP-B2.

**Creates** `internal/auth/{pairing.go,token.go,middleware.go,tlscert.go}` + tests,
`internal/httpapi/handlers_pair.go`, `cmd/homesinkd/cmd_pair.go`.

**Spec.**
- `GeneratePairingCode()` → 6 digits from `crypto/rand` (reject modulo bias), SHA-256 stored, 10 min TTL,
  single use, 5 attempts then 15 min lockout keyed by client IP.
- Token `hs_` + base64url(32 random bytes). Stored as Argon2id (`t=1, m=64MiB, p=4`).
  Verification uses a **constant-time** compare; look up by a fast SHA-256 index column, then verify Argon2id.
- `EnsureTLS()` on first run: EC P-256 self-signed, 10 years, SANs = `localhost`, `homesink.local`,
  every non-loopback IP found on the box. Persist key+cert in `server_meta`. Return the base64 SPKI SHA-256.
- Middleware: extract bearer, verify, reject revoked, load device into request context, update
  `last_seen_at` and `app_version_code` **at most once per minute per device** (not every request).

**Acceptance.** Wrong code 5× → 429 with `retryAfterMs` · expired code → 410 · a used code → 400 ·
revoked token → 401 on the next request · cert survives restart and the SPKI is stable · token is
never written to a log at any level (assert with a log-capturing test).

---

## WP-B4 — Ingest: preflight, chunks, commit · Tier L

**Goal.** The upload protocol of `02-API.md §4`. The most correctness-critical package in the backend.
**Depends on** WP-B1, WP-B2, WP-B5 (interface only), WP-B6 (interface only).

**Creates** `internal/ingest/{preflight.go,upload.go,commit.go,locks.go,sweeper.go}` + tests,
`internal/httpapi/handlers_ingest.go`.

**Spec.**
- **Preflight** (≤500 items): one indexed batch query over `blobs`, one over `upload_sessions`. No
  per-item file I/O except the existence check that guards a `duplicate` verdict (D-07). Disk guard
  first (D-20). Mime allowlist (D-14) and size cap (D-21) → `reject`.
  A `duplicate` verdict **creates the item** via `library.Place` in the same request.
- **Chunks.** `PUT` with `Content-Range`. Parse strictly; `start != received_bytes` → 409
  `RANGE_MISMATCH` with the correct `X-Homesink-Received`. Append to `staging/<hash>.part` with
  `O_APPEND`, `fsync` at chunk end, then update `received_bytes`. **In that order** — a crash may lose
  the counter update (client resends a chunk, harmless) but must never claim bytes it does not have.
- **Commit.** Re-hash the staged file streaming (never load into memory). Mismatch → delete staged file,
  delete session row, 409. Match → `library.Place` → `rename(2)` → insert blob+item+stats in one tx →
  enqueue `thumbnail` (and `transcode` if video, unless D-29 says skip) → 201.
- **Locks.** `keyedMutex` on hash for upload/commit, and on `rel_path` for placement. Concurrency is the
  main hazard here (D-16, §12).
- **Sweeper.** Hourly: staged files older than 7 days and their session rows (❓3). Also delete orphan
  `.part` files with no session row.

**Acceptance.** Interrupt at every chunk boundary of a 3-chunk upload and resume → identical final file ·
flip one byte in staging before commit → 409, staged file gone, no blob/item row · two goroutines
committing the same hash concurrently → exactly one blob, two items, no error · out-of-order chunk →
409 carrying the right offset, and the client-side recovery in one retry · a 500 MB upload never exceeds
16 MB RSS (measured, not asserted by eye).

---

## WP-B5 — Library placement · Tier M

**Goal.** Turn a `core.Placement` into a final path, safely. Pure logic + one atomic move.
**Depends on** WP-B1, WP-B2.

**Creates** `internal/library/{sanitize.go,place.go,paths.go}` + tests.

```go
func SanitizeAlbum(raw string) string                  // D-10, pure, no I/O
func BuildRelPath(album string, capturedAtMs int64, offsetMin int, filename string) string   // D-11
func (p *Placer) Place(ctx, blobHash string, pl core.Placement) (core.Item, error)           // D-13
```

**Spec.** Sanitisation exactly as D-10, in that order. `BuildRelPath` applies the offset before
extracting year/month and zero-pads the month. `Place` resolves collisions per D-13 and must
**verify the final path is still inside the library root** after all string manipulation
(defence against a hostile `album` or `filename`).

**Acceptance.** Table test covering: `../../etc`, `CON`, `  .hidden  `, a 300-char album, emoji, an
empty string, `Camera/Sub`, NFD vs NFC forms of the same umlaut (must collapse to one album) ·
23:30 on 31 Dec at `+120` lands in `2025/12`, and the same instant at `-600` lands in `2026/01` ·
same name + same hash → no new file; same name + different hash → `__<8hex>` suffix, and repeating the
operation yields the **same** name · escaping the root is impossible for any input.

---

## WP-B6 — Persistent job queue · Tier M

**Goal.** A crash-safe worker pool. Used by thumbnails and transcoding; knows nothing about media.
**Depends on** WP-B1, WP-B2.

**Creates** `internal/jobs/{queue.go,worker.go,handler.go}` + tests.

```go
type Handler interface { Kind() string; Run(ctx context.Context, payload []byte) error }
func (q *Queue) Enqueue(ctx, kind string, payload any, priority int) error
func (q *Queue) Register(h Handler)
func (q *Queue) Start(ctx context.Context)   // blocks until ctx done, drains gracefully
```

**Spec.** Claim with `UPDATE … SET state='running' WHERE job_id = (SELECT … ORDER BY priority,
next_attempt_at LIMIT 1) RETURNING *` — atomic, no double-claim. Retry 3× with backoff
1 min → 5 min → 30 min, then `failed`. On `Start`, reset every `running` row to `queued` (crash
recovery). Workers = `HOMESINK_WORKERS` or `max(1, NumCPU/2)`; the process is `nice`d in the
container entrypoint, not in Go.

**Acceptance.** Kill mid-job → the job runs again after restart, exactly once more · 4 workers × 200 jobs
→ each executes exactly once (`-race`) · a panicking handler fails that job and does not take down the
pool · priority 10 jobs consistently precede priority 100 jobs.

---

## WP-B7 — Thumbnails · Tier M

**Goal.** Generate and serve thumbnails for every media type through ffmpeg (D-31).
**Depends on** WP-B6.

**Creates** `internal/media/{ffmpeg.go,probe.go,thumbnail.go}` + tests,
`internal/httpapi/handlers_thumbs.go`.

**Spec.**
- `ffmpeg.go`: one place that builds `exec.CommandContext`, applies a timeout (60 s thumb / 6 h
  transcode), captures stderr into the error, and refuses shell interpolation. Every argument is a
  separate slice element — never a formatted string.
- Image → `-vf scale='min(N,iw)':-2` → WebP q80. Video → seek to `min(duration*0.1, duration-0.1)` or
  1.0 s. Audio → `-an -vcodec copy` on the attached picture; absent → return `ErrNoThumbnail`, which is
  a normal outcome and must not mark the job failed.
- Serving: `ETag: "<hash>-<size>"`, `Cache-Control: public, max-age=31536000, immutable`, honour
  `If-None-Match` → 304. Not yet generated → `202` + `Retry-After: 2` (the client shows a placeholder).

**Acceptance.** Golden-file tests for JPEG, PNG, HEIC, a portrait video, an MP3 with cover art, an MP3
without · a corrupt file fails the job cleanly without leaving a temp file · ffmpeg exceeding its
timeout is killed and the process group reaped (no zombies) · repeated GET serves 304.

---

## WP-B8 — Video transcoding · Tier L

**Goal.** Shrink videos to 2K without ever destroying the only copy. Implements D-29, D-30.
**Depends on** WP-B6, WP-B7 (shares `ffmpeg.go`).

**Creates** `internal/media/transcode.go`, `internal/media/verify.go` + tests.

**Spec.** Skip decision, encoder flags and the **four verification gates** are specified verbatim in
D-29/D-30 — implement them literally, in order, and do not reorder for speed. Write to
`.homesink/work/<jobId>.tmp.mp4`; on success `fsync` → `rename` over the library path → unlink the
original → set `blobs.stored_hash` + `original_replaced_at` (never touch `blobs.hash`, invariant S4) →
insert the `transcode` variant row. Any gate failure: delete the temp file, keep the original, mark the
job `done` (not `failed`) with a logged reason — "not worth transcoding" is a success.
Respect `HOMESINK_KEEP_ORIGINAL_VIDEOS=true` by keeping both and recording only the variant.
Refuse to start below 10 GiB free (D-20).

**Acceptance.** A 4K 60 s sample shrinks by >40 % and gate 2 passes · a deliberately truncated output
fails gate 2 and the original survives · an already-efficient 1080p file is skipped, untouched ·
`blobs.hash` is unchanged after transcode and a re-upload of the original file still returns
`duplicate` (this is the D-08 regression test — it must exist) · SIGTERM mid-transcode leaves no temp
file and no half-written library file.

---

## WP-B9 — Browse API · Tier S

**Goal.** Albums, periods, items, media streaming.
**Depends on** WP-B2.

**Creates** `internal/httpapi/handlers_library.go`, `internal/library/browse.go` + tests.

**Spec.** Reads `album_stats` for counts (never `COUNT(*)`). Keyset pagination on
`(captured_at_ms DESC, item_id DESC)`; the cursor is base64 of those two values and must be validated,
not trusted. `GET /v1/media/{itemId}` uses `http.ServeContent` for free Range/If-Range/206 handling;
`variant=optimized` prefers the transcode when one exists.

**Acceptance.** Paging through 10 000 items yields each exactly once with no gaps when rows are
inserted concurrently · a malformed/forged cursor → 400, never a panic or a full scan · `Range:
bytes=1000-2000` returns 206 with the right bytes · album names containing `/`, `#`, `?` and spaces
round-trip correctly through the URL.

---

## WP-B10 — APK distribution · Tier S

**Goal.** Store APKs, serve the newest, advertise updates (D-33, D-34).
**Depends on** WP-B2, WP-B3.

**Creates** `internal/appdist/{store.go,latest.go}`, `internal/httpapi/handlers_app.go`,
`cmd/homesinkd/cmd_publish.go` + tests.

**Spec.** `homesinkd publish-apk <file> --version-name x.y.z --version-code N [--notes …]` copies into
`.homesink/apk/`, computes SHA-256, writes the DB row and regenerates `latest.json`. On startup, scan
the directory and reconcile. A middleware compares `X-Homesink-Client` against the max known
`version_code` and, when newer, adds `X-Homesink-Latest` to **every** response.
`/v1/app/latest` and `/v1/app/download` are unauthenticated (D-34) and rate-limited to 30/min/IP.

**Acceptance.** Publishing a lower version code does not change `latest` · the advertised SHA-256
matches the served bytes · a client at the latest version gets **no** header · Range download of the
APK resumes correctly.

---

## WP-B11 — Self-update status & GitHub check · Tier M

**Goal.** The in-app-visible half of D-35. The actual image swap is systemd's job, not the app's.
**Depends on** WP-B1, WP-B2.

**Creates** `internal/selfupdate/{github.go,status.go}`, `internal/httpapi/handlers_system.go` + tests.

**Spec.** Daily poll of `https://api.github.com/repos/<owner>/<repo>/releases/latest` (unauthenticated,
60 req/h — cache the ETag and send `If-None-Match`). Store the result; expose `/v1/system/update`.
The app never pulls or execs anything. `HOMESINK_UPDATE_CHECK=off` disables the poll entirely and the
endpoint reports `disabled`.
**`/healthz` is the rollback gate** (D-35): it must return 200 only when the DB is open, migrations have
applied, and the library root is writable — a broken deploy has to fail this check or podman will keep it.

**Acceptance.** GitHub unreachable → status `failed`, no crash, next poll still scheduled · a 304 from
GitHub does not overwrite the cached result · `/healthz` returns 503 when the data dir is made read-only.

---

## WP-B12 — Packaging & deployment · Tier M

**Goal.** Everything in `07-DEPLOYMENT.md`, working end to end on a clean Ubuntu/Mint box.
**Depends on** all backend WPs.

**Creates** `deploy/{Containerfile,homesink.container,homesink.env.example,compose.yaml,install.sh}`,
`.github/workflows/release.yml`, `backend/README.md`.

**Acceptance.** `install.sh` on a fresh Ubuntu 24.04 VM yields a running, healthy service that survives
a reboot · the image is <150 MB · a simulated bad release fails `/healthz` and podman rolls back
automatically (test it by publishing an image whose entrypoint exits 1) · the whole flow runs rootless.

---

## WP-B13 — mDNS advertiser · Tier S
**Depends on** WP-B1. **Creates** `internal/discovery/mdns.go` + test.
Advertise `_homesink._tcp.local` per D-03, re-announce on network change, withdraw on shutdown.
**Acceptance.** `avahi-browse -r _homesink._tcp` finds it with the correct TXT record · two servers on
one LAN both appear with distinct `id`.

## WP-B14 — Admin CLI, fsck, benchmarks · Tier S
**Depends on** all. **Creates** `cmd/homesinkd/cmd_{fsck,stats}.go`, `internal/store/bench_test.go`.
`fsck` re-derives `album_stats`, marks missing blobs, deletes orphan variants and staged files, and
reports without changing anything under `--dry-run` (the default). Benchmarks assert the
`01-DECISIONS.md §11` budget so a regression fails CI rather than being noticed in a year.
