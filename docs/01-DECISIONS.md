# Homesink — Decision Register

The requirements specify *what* the product does. This file resolves everything they leave open, so
that no implementer has to invent behaviour. Format: **Decision → Why → Consequence for implementers.**

Anything marked 🔶 is a judgement call with a real trade-off; if you disagree, change it *here* first.
Anything marked ❓ still needs the product owner (collected in `§13`).

---

## 1. Identity, transport, discovery

### D-01 Authentication — pairing code → bearer token 🔶
**Decision.** The server issues a 6-digit pairing code (10 min TTL, single use, max 5 wrong attempts
then 15 min lockout). The client exchanges it once for a permanent device token `hs_<43 base64url chars>`
(32 bytes of `crypto/rand`). The token is stored Argon2id-hashed server-side. Every subsequent request
carries `Authorization: Bearer hs_…`.
**Why.** The requirements are silent on auth, but "trusted LAN" does not survive contact with a guest
WiFi, a compromised IoT device, or a roommate. A pairing code is the lowest-friction thing that still
gives per-device revocation. Passwords would mean an account system nobody asked for.
**Consequence.** `WP-B3` owns issuance/verification; every handler except `/healthz`, `/v1/pair` and
`/v1/app/latest` sits behind the auth middleware. `WP-C6` owns the client side.

### D-02 Transport — HTTP/2 over self-signed TLS with SPKI pinning
**Decision.** Server generates an EC P-256 self-signed cert on first run (10-year validity, SANs for
every local IP + `homesink.local` + `localhost`). The pairing response returns the cert's
SPKI SHA-256; the client pins it via OkHttp `CertificatePinner` and pins nothing else.
`HOMESINK_TLS=off` exists for debugging and downgrades to h2c.
**Why.** HTTP/2 multiplexing is the single biggest win for "optimize communication to be as fast and
efficient as possible" — hundreds of thumbnail GETs share one connection with no head-of-line blocking.
Pinning at pairing time gives real transport security without a CA, and avoids the
`trustAllCerts` pattern that would make the LAN assumption load-bearing.
**Consequence.** Cert generation is `WP-B3`. If the server's IP changes the cert stays valid (pinning is
on the key, not the name). Rotating the cert **breaks all paired devices** — so the key is persisted in
`.homesink/db/` and backed up with the DB.

### D-03 Discovery — mDNS, with manual fallback
**Decision.** Server advertises `_homesink._tcp.local` with TXT `v=1`, `id=<serverId>`, `name=<display>`,
`tls=1`. Client browses during pairing and offers found servers; a "manually enter address" path always
remains. Once paired, the client stores `host:port` and **also** re-resolves via mDNS if the stored
address fails — this is how it survives a DHCP lease change.
**Why.** Typing an IP is the most common onboarding failure. Re-resolution turns the most common
long-term failure (router reassigns the IP) into a non-event.
**Consequence.** `WP-B13` (advertiser), `WP-C6` (browser via `NsdManager`). mDNS is often blocked
across VLANs — the manual path is not optional.

### D-04 Clock skew
**Decision.** Every response carries `X-Homesink-Time: <RFC3339>`. If the client detects >5 min skew it
records the offset and uses **its own** clock for `capturedAt` (the phone's clock made the photo) but the
**server's** clock for token/TTL reasoning.
**Why.** Year/month placement must follow the capture device; expiry logic must follow the server.

---

## 2. Content identity and dedupe

### D-05 Hash — SHA-256 of the full file
**Decision.** SHA-256, lowercase hex, over the complete byte stream. Not a partial/head hash.
**Why.** ARMv8 has SHA-256 instructions (~1 GB/s via Conscrypt on any modern phone), so a 4 GB video
hashes in ~4 s — acceptable, and it happens once. BLAKE3 is faster but needs a third-party dependency
on both sides for no user-visible gain. Partial hashing would make the "validate the hash after upload"
requirement meaningless.
**Consequence.** The client caches the hash in Room keyed by `(mediaStoreId, size, dateModified)` so a
rescan never re-hashes. `WP-C4`.

### D-06 Blob vs Item — content is separate from placement
**Decision.** A **blob** is bytes, identified by hash. An **item** is one placement of a blob at
`Album/Year/Month/filename`. One blob may have many items (same photo, two albums, two phones).
**Why.** Without this split, the second device to upload a shared photo either gets a false "already
synced" for an album it never populated, or forces a redundant transfer.
**Consequence.** Dedupe checks `blobs`; the library tree is built from `items`. See `03-DATA-MODEL.md`.

### D-07 Dedupe hit counts as success 🔶
**Decision.** Preflight verdict `duplicate` **creates the item server-side immediately** and returns its
`itemId`. The client treats this as a successful sync, which means an "upload and remove from device"
file **is deleted locally** on a dedupe hit.
**Why.** The requirement is "only delete a local file if the backend server returned a successful status
for that file". A confirmed byte-identical copy already in the sink is the strongest possible success. The
alternative — re-uploading gigabytes to earn the right to delete — is the opposite of the efficiency goal.
**Consequence.** `duplicate` responses must be as trustworthy as a fresh commit: they are only returned
when `blobs.state = 'stored'` and the backing file passes an existence check. Never for a staged blob.

### D-08 Hash survives transcoding
**Decision.** `blobs.hash` is forever the hash of **what the client uploaded**. When the transcoder
replaces the original, the new on-disk hash goes in `blobs.stored_hash` and `original_replaced_at` is set.
**Why.** If transcoding overwrote the dedupe key, every already-synced video would look new to the next
phone and be re-uploaded in full. This one column prevents the single worst efficiency bug in the design.

---

## 3. Library placement

### D-09 Album name comes from the device folder
**Decision.** Album = Android `MediaStore.MediaColumns.BUCKET_DISPLAY_NAME` (`Camera`, `Screenshots`,
`WhatsApp Images`, …). Missing/empty → `Unsortiert`.
**Why.** It is the only album-like grouping Android exposes without extra permissions, and it matches
what the user already sees in their gallery.

### D-10 Album sanitization is server-side and strict
**Decision.** The server (never the client) normalises: Unicode NFC → strip `/ \ : * ? " < > |` and
C0 controls → collapse whitespace → trim leading/trailing dots and spaces → reject the Windows reserved
names (`CON`, `PRN`, `AUX`, `NUL`, `COM1-9`, `LPT1-9`, case-insensitive) by suffixing `_` → truncate to
64 chars on a grapheme boundary → empty result becomes `Unsortiert`.
**Why.** Server-side because the client is not the only possible client and must not be trusted with
path construction. Windows-reserved names matter because the drive is very likely reachable over SMB.
**Consequence.** Pure function, no I/O, exhaustively unit-testable — `WP-B5`, table-driven tests required.

### D-11 Year/month use the capture-local timezone
**Decision.** Client sends `capturedAtEpochMs` **and** `capturedAtOffsetMin`. The server derives
`YYYY`/`MM` by applying the offset, not by using its own zone or UTC.
**Why.** A photo taken at 23:30 on 31 December in Berlin must land in `2025/12`, not `2026/01`. Using
server-local time would also mean the same photo files differently depending on where the server sits.
**Consequence.** `MM` is zero-padded (`01`…`12`). The `03` in `2025/03` sorts correctly in every file
manager — that is why it is not `3`.

### D-12 Capture timestamp source, in order
**Decision.** `MediaStore.DATE_TAKEN` → EXIF `DateTimeOriginal` (images, when DATE_TAKEN is null) →
`DATE_MODIFIED` → `DATE_ADDED`. The offset comes from EXIF `OffsetTimeOriginal` when present, else the
device's current zone offset **at the capture instant** (not now — DST matters).
**Why.** `DATE_TAKEN` is null surprisingly often for files that arrived via messengers or downloads.

### D-13 Filename collisions
**Decision.** Same directory + same basename + **same hash** → it is the same file, no write, dedupe.
Same basename + **different hash** → `IMG_0001__a1b2c3d4.jpg` (double underscore + first 8 hex of the hash).
**Why.** `IMG_0001.jpg` from two different phones is routine. A hash suffix is deterministic (a retry
produces the same name, so retries never fan out into `_1`, `_2`, `_3`) and stays human-readable.

### D-14 Media type allowlist
**Decision.** Accept `image/*` (jpeg, png, webp, heic, heif, gif, avif, dng), `video/*` (mp4, quicktime,
3gpp, webm, x-matroska), `audio/*` (mpeg, mp4, aac, ogg, opus, flac, wav, amr, 3gpp). Anything else →
`UNSUPPORTED_MEDIA_TYPE` at preflight, cheaply, before a byte moves.
**Why.** The requirement is media only. Rejecting at preflight rather than commit is the efficiency-correct
place. Motion photos / Live Photos are ordinary JPEG or MP4 to MediaStore and need no special handling —
their embedded video simply rides along inside the file.

### D-15 Files the client offers by default 🔶
**Decision.** All media buckets are scanned and offered. There is **no** hidden exclusion of
`Screenshots`, `WhatsApp`, or download folders — but Settings gets a per-album "Ordner ignorieren" list,
empty by default.
**Why.** The requirement says all pictures, videos and audio recordings. Silently dropping folders would
lose data the user believed was backed up. Making it visible and opt-out respects both.

### D-38 A second placement of one blob is a hardlink 🔶
**Decision.** The first placement of a blob materialises the file, and `blobs.rel_path` records it as the
canonical copy. Every further `items` row for the same blob — the same photo in two albums, or the same
photo from two phones (D-06), including the `duplicate` verdict that creates an item with no transfer at
all (D-07) — is a **hardlink** to that canonical file. `library.Place` creates it under a temporary name
in `.homesink/staging/` and `rename(2)`s it into position, so a second placement is as atomic as the
first. A `rename(2)` of the staged upload remains the ordinary commit path.
**Why.** Principle 5 — the library is human-browsable — means the second album has to *contain* the photo:
someone who plugs the drive into a laptop must find it under both names. Two alternatives were weighed
and rejected. A record-only item would leave `Urlaub/2025/12/IMG_0001.jpg` in the database with nothing
behind it, which contradicts invariant S6 and makes the browsable tree quietly incomplete. A byte-for-byte
copy would be browsable but would defeat content addressing outright — one photo shared by four phones
would cost four times the space, which is the opposite of D-16's efficiency goal. A hardlink is the only
option that costs no bytes and still produces a real file.
**Consequence.** `library/` and `.homesink/` must be one filesystem. That was already required for the
atomic-rename property (§5), but it is now load-bearing for a second, independent reason. The sink
filesystem must also support hardlinks: ext4, XFS and btrfs do; exFAT and FAT32 do not, so a drive
formatted for Windows portability is not a valid sink. `07-DEPLOYMENT.md` does not yet state a filesystem
requirement and should. Deleting one item's file frees no space while another item still links the same
inode, so `homesinkd fsck` must reason about link counts rather than paths before it reports reclaimable
bytes. `WP-B5` owns the implementation; `WP-B4` calls it for both the commit and the `duplicate` path.

---

## 4. The upload protocol

### D-16 Batch preflight, content-addressed uploads
**Decision.** One `POST /v1/preflight` carries up to **500** items and returns a per-item verdict
(`upload` / `duplicate` / `resume` / `reject`). Bytes go to `PUT /v1/blobs/{hash}` with `Content-Range`.
There is **no upload session**: the hash *is* the upload id.
**Why.** This is the core of "optimize the communication to be as fast and efficient as possible". A
1000-photo sync costs 2 preflight round trips instead of 1000. Content addressing makes every upload
idempotent and resumable with no server-side session state to expire, lose, or clean up.
**Consequence.** Two devices uploading the same blob concurrently must be serialised by a per-hash lock
server-side (`WP-B4`). The loser gets `duplicate` on commit, not an error.

### D-17 Chunk size 8 MiB, 3 concurrent files, WiFi-only by default
**Decision.** 8 MiB chunks; 3 files in flight; sequential chunks within a file. Sync requires unmetered
network unless the user enables "Auch über mobile Daten" (default **off**). No charging requirement.
**Why.** 8 MiB balances re-transmit cost on a flaky link against per-request overhead. 3 concurrent
streams saturates typical home WiFi without starving the UI thread. Metered-off by default because
silently uploading 40 GB of video over LTE is the worst thing this app could do to someone.

### D-18 Verification failure destroys the staged file
**Decision.** On commit the server re-hashes the staged file itself. Mismatch → HTTP 409 `HASH_MISMATCH`,
staged file deleted, `upload_sessions` row deleted. The client re-hashes locally **once** and retries; a
second mismatch fails that file permanently for the run and is surfaced in the queue screen.
**Why.** Literally the requirement ("return an error to the client and remove the file from the backend
system"). The single client-side re-hash covers the real cause — the file changed on the device between
hashing and upload — without an infinite loop.

### D-19 Retry policy
**Decision.** Exponential backoff 1s → 2 → 4 → 8 → 16 → 30s cap, full jitter, max 5 attempts per file per
run. Retryable: network errors, 5xx, 429, `RANGE_MISMATCH` (re-`HEAD` then continue). Not retryable:
401 (→ re-pair), 413, 415, 507. `HASH_MISMATCH` gets exactly one re-hash as per D-18.

### D-20 Disk-space guard
**Decision.** Preflight rejects the whole batch with 507 `INSUFFICIENT_STORAGE` if
`free < sum(batch) + 5 GiB` headroom. The transcoder refuses to start a job below 10 GiB free.
**Why.** Filling the sink drive corrupts SQLite and loses data. Failing early and loudly is the only
acceptable behaviour, and the headroom exists because transcoding needs scratch space.

### D-21 Size cap
**Decision.** 16 GiB per file, configurable via `HOMESINK_MAX_FILE_BYTES`.
**Why.** Guards against a pathological upload eating the drive; well above any phone-recorded video.

---

## 5. Android-specific behaviour the requirements could not know about

### D-22 Deleting a local file needs the user's consent, in a batch 🔶
**Decision.** The "upload and remove from device" deletions are **accumulated across the whole sync run**
and executed as **one** `MediaStore.createDeleteRequest()` at the end. Files whose upload succeeded but
whose deletion the user declined are marked `SYNCED_KEPT` and never re-offered for deletion.
Settings offers an opt-in for the `MANAGE_MEDIA` special permission ("Löschen ohne Nachfrage erlauben"),
which removes the dialog entirely on Android 12+.
**Why.** This is the biggest hidden requirement in the project. Since Android 11 an app **cannot** delete
media it did not create without a system consent dialog. Per-file dialogs during a 200-file sync would be
unusable; one dialog at the end is the only humane design. `MANAGE_MEDIA` cannot be the default because it
is a Settings-panel special permission that most users will decline.
**Consequence.** The delete step needs an `Activity` (an `IntentSender` cannot be launched from a
service). If the app is backgrounded when the sync finishes, the manager posts a "Bereit zum Löschen"
notification and defers until it is next opened. `WP-C10` owns this; it is the highest-risk client package.

### D-23 Permission matrix
| SDK | Needed |
|---|---|
| 26–32 | `READ_EXTERNAL_STORAGE` |
| 33+ | `READ_MEDIA_IMAGES`, `READ_MEDIA_VIDEO`, `READ_MEDIA_AUDIO`, `POST_NOTIFICATIONS` |
| 34+ | `FOREGROUND_SERVICE_DATA_SYNC` + `foregroundServiceType="dataSync"` |
| all | `ACCESS_MEDIA_LOCATION` (preserve GPS EXIF), `INTERNET`, `REQUEST_INSTALL_PACKAGES` |
| 31+ opt-in | `MANAGE_MEDIA` |

**Decision.** minSdk **26**, targetSdk **36**. Android 14's *partial* media access
(`READ_MEDIA_VISUAL_USER_SELECTED`) is handled by detecting it and showing a persistent hint that
Homesink can only see selected files — not by pretending the library is complete.
**Why.** minSdk 26 covers >98% of devices and gives notification channels + `PackageInstaller` unconditionally.

### D-24 Doze, and why the notification is not a `setExactAndAllowWhileIdle` alarm
**Decision.** The daily decision runs in a `PeriodicWorkRequest` (6 h, flex 1 h) plus a
`OneTimeWorkRequest` scheduled for the next chosen slot with a ±15 min tolerance. Sync itself runs in a
foreground service started from the notification tap.
**Why.** Exact alarms now require a special permission and are meant for calendars, not for "would you
like to back up". A 15-minute window is invisible to the user and survives Doze without begging for
privileges. See `06-ALGORITHMS.md §1`.

### D-25 Notification channels (Android requires these to be named up front)
| Channel id | German name | Importance | Used for |
|---|---|---|---|
| `sync_prompt` | „Synchronisierung vorschlagen" | DEFAULT | The daily/weekly ask |
| `sync_progress` | „Synchronisierung läuft" | LOW | Foreground-service progress, silent, no badge |
| `sync_result` | „Synchronisierung abgeschlossen" | LOW | Summary + "Bereit zum Löschen" |
| `sync_problem` | „Probleme bei der Synchronisierung" | DEFAULT | Auth lost, server unreachable, failures |
**Why.** Separate channels let the user mute the chatty ones without muting the ones that matter — which
is the concrete mechanism behind "do not annoy the user".

---

## 6. Notification policy

### D-26 The exact decision rule
Evaluated once per day at the scheduled slot (`06-ALGORITHMS.md §1` derives the slot):

```
if alreadyNotifiedToday          -> skip     # hard "at most once a day" cap
if syncCurrentlyRunning          -> skip
if pendingCount == 0             -> skip
if pendingCount >= threshold     -> NOTIFY   (any day)
if isSunday && createdThisWeek>0 -> NOTIFY   (weekly catch-up)
else                             -> skip
```

**Decisions embedded here.**
- **"Since the last synchronization"** = files with `state = PENDING`, i.e. never successfully synced.
  Not "since the last sync *run*" — a file the user deselected stays pending and keeps counting.
- **Threshold default 10**, range 1–500, in Settings.
- **"End of week" = Sunday**, ISO-8601 (Mon–Sun), device-local. German locale, so Sunday is the week's end.
- **`createdThisWeek`** counts pending files whose `capturedAt` falls in the current ISO week. This is the
  requirement "do not notify if no files have been created that week": old leftovers alone never trigger
  the *weekly* notification, though they still trigger the *threshold* one.
- **Precedence:** threshold wins; only one notification either way.
**Why the two rules can both be true and it doesn't matter.** Both paths post the same notification, and
the once-a-day guard runs first, so there is no double-notify path to test.

### D-27 What counts as an "interaction" for learning
| Event | Weight | Recorded when |
|---|---|---|
| `NOTIFICATION_TAPPED` | +3.0 | User opens the sync list from the prompt |
| `SYNC_STARTED` | +3.0 | User presses „Jetzt synchronisieren" |
| `APP_OPENED_DIRECT` | +1.0 | Launcher/app-icon open, not from a notification |
| `NOTIFICATION_DISMISSED` | −1.0 | Swiped away |
| `NOTIFICATION_IGNORED` | −0.5 | Still showing, untouched, 4 h later |

**Why negatives.** Without them the model only learns *when the user is awake*, not when they are
*receptive*. Dismissals are the only signal that a time is actively wrong.

### D-28 Learned times are clamped to 08:00–23:00
**Decision.** Never schedule outside that window regardless of what the data says. Cold-start default is
**22:00** as specified.
**Why.** A user who habitually taps notifications at 02:40 should not be prompted at 02:40; the data would
support it and the user would hate it. This is a guardrail, not a learned parameter.

---

## 7. Media processing

### D-29 Transcode target: HEVC, ≤2560 px wide, CRF 24 🔶
**Decision.**
```
-c:v libx265 -crf 24 -preset medium -pix_fmt yuv420p
-vf "scale='min(2560,iw)':-2:flags=lanczos"      # never upscale, keep aspect, even dims
-c:a aac -b:a 128k -movflags +faststart -map_metadata 0 -tag:v hvc1
```
Skipped entirely when the source is already ≤2560 px **and** below `0.12 bpp`
(`bitrate / (w*h*fps)`) — re-encoding an efficient file only loses quality.
**Why.** HEVC at CRF 24 lands ~50 % of an H.264 phone capture at visually transparent quality for
2K playback, which is the stated goal. `-tag:v hvc1` is required or Apple-derived players show black.
`+faststart` matters because the app streams over HTTP. `HOMESINK_VIDEO_CODEC=h264` exists for
anyone with a device that cannot hardware-decode HEVC.

### D-30 Verify before destroying the original — the four gates
The original is deleted **only** when all four pass; any failure keeps the original and discards the
transcode:
1. `ffprobe` reports ≥1 video stream and a duration within **±1.0 s** of the source;
2. a full decode pass (`ffmpeg -v error -i out -f null -`) exits 0 with **empty** stderr;
3. output size **< 90 %** of the original (otherwise the transcode is pointless);
4. the output is `fsync`'d and atomically renamed into place before the original is unlinked.
**Why.** The user chose "replace original". Combined with "upload and remove from device", the transcode
can become the **only surviving copy** of a family video. A size check alone would happily keep a
truncated file that ffmpeg wrote before dying. Gate 2 is the expensive one and it is non-negotiable.

### D-31 Everything goes through ffmpeg, including image thumbnails
**Decision.** Thumbnails for images, videos and audio cover art are all produced by ffmpeg. Two sizes,
WebP q=80: **256 px** (grids) and **1024 px** (tap-to-preview before the original streams).
Video poster = frame at 10 % of duration (or 1.0 s if shorter). Audio = embedded cover art, else no
thumbnail and the client draws an icon.
**Why.** One code path instead of a Go image stack plus a HEIC/AVIF/DNG decoder — Samsung and Pixel
phones ship HEIC by default and `image/jpeg` alone would fail on a large share of a real library.
**Consequence.** ffmpeg is a hard runtime dependency, pinned in the image. `WP-B7`.

### D-32 Job queue is persistent, and thumbnails outrank transcodes
**Decision.** Jobs live in SQLite (`kind`, `priority`, `attempts`, `next_attempt_at`). Worker pool of
`max(1, NumCPU/2)`, `nice 10`. Thumbnails priority 10, transcode priority 100 (lower runs first).
Jobs `RUNNING` at startup are reset to `QUEUED` (crash recovery). Max 3 attempts, then `FAILED` and
visible on the admin endpoint.
**Why.** Transcoding a 4 GB video takes 20 minutes; if it blocked thumbnail generation the browse screen
would be empty for the whole time. Half the cores keeps the box usable for whatever else it does.

---

## 8. Client update flow

### D-33 Version is advertised on every response, not polled
**Decision.** Client sends `X-Homesink-Client: <versionCode>/<versionName>` on every request. When a newer
release exists the server attaches `X-Homesink-Latest: <code>;<name>;<sha256>;<url>` to **any** response.
`GET /v1/app/latest` also exists for the explicit check.
**Why.** Zero extra round trips for a check that would otherwise happen on every app start — directly in
service of the efficiency requirement.

### D-34 Banner, download, verify, `PackageInstaller`
**Decision.** A dismissible-per-version banner at the top of every screen (never a notification, per the
requirement). Tap → download to app-private storage → **verify SHA-256 against `latest.json`** → hand to
`PackageInstaller`. `versionCode` comparison only; `versionName` is cosmetic.
**Why.** Verifying the hash before invoking the installer is the difference between an update channel and
a malware channel, given a self-signed LAN server.
**Consequence.** Every release must be signed with **the same keystore** or Android refuses the update as
a different app. The keystore is a project asset — losing it means every user must uninstall/reinstall.
See `07-DEPLOYMENT.md §5`.

---

## 9. Server self-update

### D-35 Podman quadlet + `podman auto-update` with a health gate 🔶
**Decision.** The container carries `io.containers.autoupdate=registry`. A systemd timer pulls a new image
daily; systemd starts it, the unit's health check must pass within 90 s or **podman rolls back to the
previous image automatically**. The app additionally exposes `GET /v1/system/update` showing current
version, latest seen on GitHub, and last update result, so the phone can display it.
**Why.** This satisfies "automatically updates itself" without granting anything a Docker socket (which is
root-equivalent on the host). Rollback-on-failed-health is the property Watchtower lacks and the reason
this beats the more familiar option. Alternatives analysed in `07-DEPLOYMENT.md §3`.
**Consequence.** Migrations must be **forward-only and safe to roll back from** — never drop or rewrite a
column an older binary still reads. `WP-B2` enforces this as a review rule.

### D-36 Database backups
**Decision.** Nightly `VACUUM INTO .homesink/backups/homesink-<date>.db`, keep 7, plus one before every
schema migration.
**Why.** The library files survive anything, but losing the DB loses albums, dedupe state and pairings.
A `VACUUM INTO` snapshot is consistent under WAL without stopping the server.

---

## 10. Localization

### D-37 German is the base locale
**Decision.** `values/strings.xml` **is** German — no English default, no `values-de`. Every user-visible
string is a resource; hard-coded UI text fails review. Dates/numbers via `java.time` + `NumberFormat`
with the device locale. Formatted counts use positional args (`%1$d`), never concatenation.
**Why.** Base-locale German means the app is correct on a device set to any language, which an English
default plus `values-de` would not guarantee. Keeping it resource-based costs nothing now and is the
only thing that makes a second language later a translation task instead of a rewrite.
**Consequence.** The canonical strings for the screens the requirements specify verbatim are fixed in
`05-WORKPACKAGES-CLIENT.md §2`.

---

## 11. Efficiency budget (what "fast and efficient" is measured against)

| Operation | Budget | Mechanism |
|---|---|---|
| Preflight, 500 items | ≤ 400 ms | one request, indexed hash lookup, no I/O per item |
| Thumbnail GET (warm) | ≤ 15 ms | content-addressed, `Cache-Control: immutable, max-age=31536000`, ETag |
| Sustained upload | ≥ 80 % of link | 8 MiB chunks, 3 streams, no multipart, no gzip on media |
| Album list | ≤ 100 ms @ 100k items | denormalised counts, no `COUNT(*)` at read time |
| Browse page | ≤ 150 ms | keyset pagination (never `OFFSET`) |
| Idle server RSS | ≤ 80 MB | Go, pure-Go SQLite, no in-process image stack |
**Why these numbers.** They are the thresholds at which the UI stops feeling instant; they are asserted in
the benchmark suite (`WP-B14`), not just aspirational.

**JSON is gzip'd; media never is.** Re-compressing JPEG/H.264 burns CPU on both ends for ~0 % gain.

---

## 12. Concurrency and multi-device

**Decision.** Multiple paired phones may sync simultaneously. Serialisation points: a per-hash upload
lock (D-16), a per-`(album,year,month,filename)` placement lock, and SQLite in WAL with
`busy_timeout=5000`. There is **no** per-user isolation — every device sees the whole library. Single
household, as specified.

---

## 13. ❓ Open questions for the product owner

None of these block implementation; each has a stated default that ships if unanswered.

1. **Album for messenger media.** `WhatsApp Images` etc. currently become their own albums. Fold them
   under one `Messenger` album instead? *Default: keep them separate.*
2. **Audio grouping.** Voice recordings have no meaningful "album" and land in `Unsortiert` or
   `Recordings`. Give audio a dedicated top-level `Sprachaufnahmen` album? *Default: bucket name, as with everything else.*
3. **Retention of failed uploads.** Staged `.part` files from abandoned uploads are currently swept after
   **7 days**. Shorter/longer? *Default: 7 days.*
4. **GitHub repository and image registry** for D-35 self-update — the repo/registry path must exist
   before `WP-B11`/`WP-B12` can be finished. *No default possible; needed before release.*
5. **Server timezone.** Used for nightly jobs and log timestamps only (placement uses D-11).
   *Default: `TZ=Europe/Berlin`.*
