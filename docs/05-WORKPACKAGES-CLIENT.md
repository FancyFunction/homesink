# Client Work Packages (Kotlin / Jetpack Compose)

Same rules as `04-`: one package = one assignment, read only this section + `01-DECISIONS.md` + the
listed dependencies, never edit another package's files (`08-ROADMAP.md §4`).

**Stack.** Kotlin 2.0, Compose BOM (Material 3), Hilt, Room, WorkManager, OkHttp 4 + Retrofit 2 +
kotlinx-serialization, Coil 3 (thumbnails), DataStore Preferences, `androidx.security` for the token.
minSdk 26, targetSdk 36 (D-23). Single Gradle module `:app` (`00-ARCHITECTURE.md §4.1`).

**Definition of done, every package:** compiles, `./gradlew lint` clean, unit tests pass, no hard-coded
user-visible string, no `!!` on a nullable that can be null in production, acceptance criteria each
covered by a named test.

---

## 1. Package map

```
de.homesink.app
├── contract/     FROZEN after WP-C1 — pure Kotlin, no Android imports
├── data/db/      Room entities, DAOs, converters             (WP-C2)
├── data/net/     Retrofit API, DTOs, interceptors, pinning   (WP-C5)
├── data/repo/    repositories bridging db ↔ net              (WP-C2/C5)
├── media/        MediaStore scanner, hasher, deletion        (WP-C3, C4, C10)
├── sync/         WorkManager, foreground service, engine     (WP-C8)
├── notify/       channels, scheduler, learning               (WP-C9, C11)
├── update/       banner state, APK download + install        (WP-C14)
├── ui/           theme, nav, one sub-package per screen      (WP-C1, C7, C9, C12, C13)
└── di/           one Hilt module per feature package
```

---

## 2. German strings — canonical wording

The requirements name several strings verbatim. These keys are fixed; do not paraphrase them, and do not
invent a second key for the same concept.

| Key | German |
|---|---|
| `nav_sync_list` | Synchronisieren |
| `nav_browser` | Sink |
| `nav_queue` | Warteschlange |
| `nav_settings` | Einstellungen |
| `sync_now` | Jetzt synchronisieren |
| `sync_progress_fmt` | `%1$d %% erledigt – %2$d/%3$d Dateien synchronisiert` |
| `notif_prompt_title` | Neue Dateien zum Sichern |
| `notif_prompt_body_fmt` | `%1$d neue Dateien warten auf die Synchronisierung` |
| `notif_ready_to_delete` | Bereit zum Löschen |
| `settings_threshold` | Benachrichtigen ab |
| `settings_custom_schedule` | Eigener Synchronisierungsplan |
| `settings_custom_schedule_desc` | Lege fest, wann du zum Synchronisieren deiner Dateien gefragt werden möchtest |
| `settings_same_time_every_day` | Gleiche Uhrzeit für jeden Tag |
| `settings_wifi_only` | Nur über WLAN synchronisieren |
| `settings_allow_manage_media` | Löschen ohne Nachfrage erlauben |
| `settings_ignored_albums` | Ordner ignorieren |
| `mode_upload` | Hochladen |
| `mode_upload_delete` | Hochladen und vom Gerät löschen |
| `update_banner_fmt` | `Version %1$s ist verfügbar` |
| `update_banner_action` | Aktualisieren |
| `weekday_1…7` | Montag … Sonntag |
| `album_unsorted` | Unsortiert |

`%%` in `sync_progress_fmt` is a literal percent sign — the `%1$d %%` pattern renders as `29 %`, which is
the correct German spacing (unlike English `29%`).

---

## WP-C1 — Skeleton, theme, navigation · Tier S

**Goal.** An installable app with four working tabs and the exact colour scheme. No features.

**Creates** the Gradle setup, `ui/theme/{Color.kt,Theme.kt,Type.kt}`, `ui/nav/{HomesinkNav.kt,Destinations.kt}`,
`MainActivity.kt`, `HomesinkApp.kt` (`@HiltAndroidApp`), `res/values/strings.xml` (German base, D-37),
and **`contract/` — frozen after this WP**:

```kotlin
package de.homesink.app.contract
enum class MediaType { IMAGE, VIDEO, AUDIO }
enum class SyncMode  { UPLOAD, UPLOAD_AND_DELETE }
enum class ItemState { DISCOVERED, HASHED, QUEUED, UPLOADING, SYNCED,
                       SYNCED_DELETED, SYNCED_KEPT, FAILED, SKIPPED }
data class LocalMedia(val id: Long, val mediaStoreId: Long, val uri: String, val filename: String,
                      val album: String, val mimeType: String, val mediaType: MediaType,
                      val sizeBytes: Long, val capturedAtMs: Long, val capturedOffsetMin: Int,
                      val durationMs: Long?, val hash: String?, val state: ItemState,
                      val mode: SyncMode, val selected: Boolean)
data class SyncProgress(val doneFiles: Int, val totalFiles: Int,
                        val sentBytes: Long, val totalBytes: Long, val currentFilename: String?) {
    val percent: Int get() = if (totalBytes == 0L) 0 else ((sentBytes * 100) / totalBytes).toInt()
}
interface MediaScanner   { suspend fun scan(): List<LocalMedia>; fun observe(): Flow<Unit> }
interface Hasher         { suspend fun hash(uri: String): String }
interface SyncClient     { /* mirrors 02-API.md; see WP-C5 */ }
interface DeletionManager{ suspend fun requestDeletion(ids: List<Long>): DeletionOutcome }
```

**Colour mapping** — the requirement's palette onto Material 3. Define all nine steps of each ramp as
raw `Color` values, then map:

| M3 role | Light | Dark |
|---|---|---|
| `primary` | `primary-600` #078BA1 | `primary-400` #4AC0E8 |
| `onPrimary` | `primary-100` | `primary-900` |
| `primaryContainer` | `primary-200` | `primary-800` |
| `secondary` / accent | `accent-500` #C79010 | `accent-400` #E7C045 |
| `surface` | `neutral-100` | `neutral-900` |
| `onSurface` | `neutral-900` | `neutral-200` |
| `surfaceVariant` | `neutral-200` | `neutral-800` |
| `outline` | `neutral-400` | `neutral-600` |
| `error` | #B3261E | #F2B8B5 |

The accent is used **only** for the primary action (`Jetzt synchronisieren`) and progress fills — it is
an accent, and using it for ordinary chrome destroys the hierarchy the palette is built for.

**Acceptance.** All four tabs navigate and keep their back stack independently · light and dark both
meet WCAG AA (4.5:1) for body text — assert the computed contrast in a unit test, not by eye ·
no string literal in any composable · rotation preserves the selected tab.

---

## WP-C2 — Room database · Tier S
**Depends on** WP-C1. **Creates** `data/db/**` — entities exactly as `03-DATA-MODEL.md §2.1`, DAOs,
`HomesinkDatabase`, migrations, `di/DatabaseModule.kt`. Export schemas to `app/schemas/`.
DAOs return `Flow` for anything the UI observes and `suspend` for one-shot writes.
Needed queries: pending count, count captured in the current ISO week, paged items by state,
update state/progress, the interaction-event insert and the aggregate the learner reads.
**Acceptance.** Room schema export is committed · in-memory DB test for every DAO method ·
`pendingCount()` matches the D-26 definition of PENDING exactly (this is a real test, not a formality).

## WP-C3 — MediaStore scanner + permissions · Tier M
**Depends on** WP-C1, WP-C2. **Creates** `media/{MediaStoreScanner.kt,PermissionState.kt}`,
`ui/permissions/`.
Query images/video/audio with the D-23 permission set. Extract every field in `LocalMedia`, applying the
D-12 timestamp fallback chain. Incremental rescan uses `DATE_ADDED > lastScan` plus a full reconcile
that removes rows whose `mediaStoreId` has disappeared. Register a `ContentObserver` to refresh.
Handle Android 14 partial access (D-23) by surfacing it, not hiding it.
**Acceptance.** A file deleted outside the app disappears from the index on rescan · `DATE_TAKEN`-null
files still get a sensible date · scanning 20 000 items completes <3 s and allocates no more than one
`LocalMedia` per row (no intermediate list copies) · denying permission shows a rationale, never a crash.

## WP-C4 — Hasher · Tier S
**Depends on** WP-C2. **Creates** `media/Sha256Hasher.kt`.
Stream through `ContentResolver.openInputStream` with a 64 KiB buffer into `MessageDigest`. Cache per
D-05/C3. Cancellable at buffer boundaries. Run on `Dispatchers.IO`, at most 2 concurrent.
**Acceptance.** Matches `sha256sum` for a known file · re-scan of an unchanged file performs **zero**
reads (assert on a counting `ContentResolver`) · cancelling mid-hash releases the stream.

## WP-C5 — Network layer · Tier M
**Depends on** WP-C1. **Creates** `data/net/{HomesinkApi.kt,Dtos.kt,Interceptors.kt,Pinning.kt,
ApiResult.kt}`, `di/NetworkModule.kt`.
Retrofit + kotlinx-serialization DTOs generated from `docs/api/openapi.yaml` field-for-field.
Interceptors: auth bearer; `X-Homesink-Client`; **`X-Homesink-Latest` parser** that publishes to a shared
`UpdateAvailability` flow (D-33); error-envelope → `core`-equivalent sealed `ApiError`.
`CertificatePinner` from the stored SPKI (D-02). OkHttp with HTTP/2, a 30 s call timeout but **no**
read timeout on upload/download calls, and a connection pool that survives the whole sync run.
**Acceptance.** MockWebServer tests replay the **same** JSON fixtures the Go tests use · a wrong
certificate fails the call (pinning actually engaged — verify with a second self-signed cert) ·
every error code in `02-API.md §2` maps to a distinct `ApiError` · a 401 clears the token exactly once.

## WP-C6 — Pairing & onboarding · Tier M
**Depends on** WP-C5. **Creates** `ui/pairing/**`, `data/repo/ServerRepository.kt`.
mDNS discovery via `NsdManager` (D-03) with a manual `host:port` fallback that is always reachable, a
6-digit code field, then store token (EncryptedSharedPreferences) + SPKI + host. Re-resolve via mDNS when
the stored address fails before showing an error.
**Acceptance.** Discovery finds a `WP-B13` server · manual entry works with mDNS blocked · a wrong code
shows the server's message and allows a retry · a rate-limit response disables the field and counts down ·
the token never appears in logs or in a crash report.

## WP-C7 — Synchronisation list screen · Tier M
**Depends on** WP-C2, WP-C3, WP-C4. **Creates** `ui/sync/**`.
The screen the notification opens. Grid/list of pending files with a Coil thumbnail from the **local**
`ContentResolver` (not the server — these files are not uploaded yet). Tapping a thumbnail opens the
full local file. Each row carries a two-state control for `Hochladen` / `Hochladen und vom Gerät löschen`,
and a selection checkbox. **All files start selected** (requirement); mode defaults come from
`06-ALGORITHMS.md §2`. A sticky bottom `Jetzt synchronisieren` button showing the selected count and
total size. Bulk actions: select all / none / invert.
**Acceptance.** Defaults are exactly D/§2 — a 31 MB video is `UPLOAD_AND_DELETE`, a 29 MB video and
**every** image is `UPLOAD` · scrolling 5 000 items stays at 60 fps (`LazyVerticalGrid` + stable keys) ·
selection survives rotation and process death · mode/selection changes persist to Room immediately.

## WP-C8 — Sync engine · Tier L
**Depends on** WP-C4, WP-C5, WP-C2. **Creates** `sync/{SyncWorker.kt,SyncEngine.kt,SyncForegroundService.kt,
UploadTask.kt}`, `data/repo/SyncRepository.kt`.
The client half of `02-API.md §4`. Batch preflight (500) → handle four verdicts → upload with 8 MiB
chunks, 3 files concurrent (D-17) → commit. Persist `uploadedBytes` from `X-Homesink-Received` after every
chunk so process death costs one chunk (invariant C2). Retry per D-19. Foreground service typed
`dataSync` with `POST_NOTIFICATIONS` handled. Emits `SyncProgress` at most **twice a second**
(notification updates are rate-limited by the system and more often is wasted work). Successful
`UPLOAD_AND_DELETE` files are appended to `pending_deletion` — and nothing else in the app writes there.
**Acceptance.** Airplane-mode mid-run then restore → resumes, no duplicate bytes, no lost file · killing
the app mid-upload → resumes from the server's offset on the next run · a `duplicate` verdict marks the
file synced without transferring bytes (D-07) · `HASH_MISMATCH` triggers exactly one re-hash then fails
that file only · WiFi-only respected (`NetworkType.UNMETERED` constraint) · cancelling stops within 2 s.

## WP-C9 — Progress notification & queue screen · Tier M
**Depends on** WP-C8. **Creates** `notify/{NotificationChannels.kt,ProgressNotifier.kt}`, `ui/queue/**`.
Ongoing notification on `sync_progress` with a determinate progress bar and
`sync_progress_fmt` → „29 % erledigt – 14/47 Dateien synchronisiert". Tapping opens the queue screen.
The queue screen shows active uploads at the top with per-file progress, then finished ones
**most-recent-first** (requirement), with failures showing a German reason and a retry action.
**Acceptance.** The notification text matches the requirement's format exactly · it clears when the run
ends and is replaced by a `sync_result` summary · the queue screen updates live without flicker and
survives process death · all four channels from D-25 are created with the right importance.

## WP-C10 — Deletion manager · Tier L
**Depends on** WP-C8. **Creates** `media/DeletionManagerImpl.kt`, `ui/deletion/DeletionActivity.kt`.
The hardest client package — read D-22 in full first. Batch every confirmed-uploaded
`UPLOAD_AND_DELETE` URI into **one** `MediaStore.createDeleteRequest`. API-level branching: ≤28 direct
delete, 29 `RecoverableSecurityException`, 30+ `createDeleteRequest`, 31+ `MANAGE_MEDIA` opt-in skips the
dialog. If the app is backgrounded, post `notif_ready_to_delete` on `sync_result` and defer to the next
foreground. Declined files → `SYNCED_KEPT`, never re-offered.
**Acceptance.** Files are deleted **only** after a confirmed server success (invariant C1 — test the
negative case: a failed upload is never queued for deletion) · declining leaves every file intact and
marks them `SYNCED_KEPT` · one dialog for 50 files, not 50 · process death between upload and consent
still deletes on next launch (`pending_deletion` survives) · with `MANAGE_MEDIA` granted, no dialog appears.

## WP-C11 — Adaptive notification scheduler · Tier L
**Depends on** WP-C2. **Creates** `notify/{ScheduleLearner.kt,NotificationDecider.kt,
NotificationWorker.kt,InteractionTracker.kt}`.
Implements `06-ALGORITHMS.md §1` literally — the algorithm is fully specified there, so this package is
mostly careful transcription plus tests. `ScheduleLearner` must be a **pure function** of
`(events, now, settings)` so it can be tested without Android at all.
**Acceptance.** With no data, the first notification is 22:00 (requirement) · a synthetic user who always
taps at 19:30 on weekdays and 11:00 at weekends converges to those slots within 3 weeks of events ·
`03:00` interactions never produce a `03:00` schedule (clamped, D-28) · never more than one notification
per calendar day across a simulated 90-day run (assert the count) · zero pending files → zero
notifications · below threshold on a Tuesday → no notification; the same state on Sunday with files from
that week → exactly one · manual override wins over everything.

## WP-C12 — Settings · Tier S
**Depends on** WP-C2, WP-C11. **Creates** `ui/settings/**`, `data/repo/SettingsRepository.kt`.
Every key in `03-DATA-MODEL.md §2.3`. The custom-schedule section behaves exactly as specified:
`Eigener Synchronisierungsplan` toggle → reveals `Gleiche Uhrzeit für jeden Tag` → enabled shows one time
picker, disabled shows seven rows Montag…Sonntag each with its own picker. Plus threshold, WiFi-only,
`MANAGE_MEDIA` opt-in (deep-links to the system panel), ignored albums, server info + re-pair, and an
about section with the app version.
**Acceptance.** Toggling reveals/hides exactly the specified controls with no intermediate broken state ·
all seven day pickers persist independently · changing any schedule setting reschedules the worker
immediately (assert against a test `WorkManager`) · threshold is clamped to 1–500.

## WP-C13 — Backend file browser · Tier M
**Depends on** WP-C5. **Creates** `ui/browse/**`, `data/repo/LibraryRepository.kt`.
Albums → year/month → item grid → viewer. Paging 3 over the keyset cursor. Coil loads
`/v1/thumbs/{hash}?s=256` with the auth header and relies on the server's immutable caching plus a disk
cache. The viewer shows `s=1024` first, then streams the original; video plays via Media3 ExoPlayer
against `/v1/media/{itemId}` (Range-capable). Handle the `202 not yet generated` thumbnail response with
a placeholder and one delayed retry.
**Acceptance.** 10 000 items scroll smoothly with a bounded memory profile · back navigation restores
scroll position at every level · video seeks work (proves Range end-to-end) · offline shows a cached
page and a clear message rather than an empty screen.

## WP-C14 — Update banner & installer · Tier M
**Depends on** WP-C5. **Creates** `update/{UpdateRepository.kt,ApkInstaller.kt}`, `ui/update/UpdateBanner.kt`.
Consumes the `UpdateAvailability` flow from WP-C5 (no polling). A banner at the **top of every screen**,
never a notification (requirement), dismissible per `versionCode` (D-34). Download with progress →
**verify SHA-256** → `PackageInstaller` session. `REQUEST_INSTALL_PACKAGES` + the "unknown sources"
deep-link when the user has not granted it.
**Acceptance.** A tampered APK (hash mismatch) is **refused and deleted**, with a German error — this
test is mandatory · dismissing hides it for that version only and it returns for the next · the banner
never covers the bottom nav or the sync button · the download resumes after an interruption.

## WP-C15 — Release build & signing · Tier S
**Depends on** all client WPs, WP-B10.
R8 with a keep rule for kotlinx-serialization; signing config from env/keystore properties (never
committed); `versionCode` from CI; reproducible-ish builds; `./gradlew assembleRelease` then
`homesinkd publish-apk`. Document the keystore-loss consequence from D-34 prominently in
`client/README.md`.
**Acceptance.** A release APK installs over a debug build only after uninstall (expected), and over an
earlier release build in place · R8 does not strip any DTO (run the full MockWebServer suite against the
minified build — this catches the serialization keep-rule bug that otherwise ships).
