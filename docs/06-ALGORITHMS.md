# Homesink — Specified Algorithms

Three behaviours in the requirements are described in prose but need exact definitions before they can
be implemented consistently. This file is that definition. `WP-C11`, `WP-C7` and `WP-B8` are
transcriptions of what follows.

---

## 1. Adaptive notification scheduling

> "Start notifying the user at 22:00 and keep track of when the user interacts with the notifications or
> opens the app directly. Based on the tracked data, find the best time to ask the user for an
> interaction. Analyze each day of the week separately."

### 1.1 Shape of the model

A weighted histogram over **48 half-hour slots × 7 days of week**. Not a clustering or ML model:
the data is tiny (a handful of events per week), it must be explainable to the user, it must run in
milliseconds on a phone, and it must be unit-testable deterministically. A histogram with decay is the
right complexity for the problem.

```
slot(minuteOfDay) = minuteOfDay / 30          →  0…47
```

### 1.2 Weighting

Each interaction event contributes `typeWeight × recencyWeight`:

```
typeWeight:   NOTIFICATION_TAPPED   +3.0
              SYNC_STARTED          +3.0
              APP_OPENED_DIRECT     +1.0
              NOTIFICATION_DISMISSED −1.0
              NOTIFICATION_IGNORED   −0.5      (still showing, untouched, after 4 h)

recencyWeight = 0.5 ^ (ageDays / 30)          30-day half-life
```
Events older than **120 days are deleted** (≈16 % weight remaining — keeping them costs storage and
buys nothing).

**Why negative weights exist.** Positive-only data learns when the user is *awake*, not when they are
*receptive*. A dismissal is the only evidence that a time is actively wrong, and without it the model
converges on whatever time the user happens to unlock their phone most — which is not the same thing.

### 1.3 Smoothing

Raw slot scores overfit to the exact minute of a handful of taps. Apply a triangular kernel over
±1 slot before choosing:

```
smoothed[s] = raw[s] + 0.5 × (raw[s−1] + raw[s+1])       (no wraparound across midnight)
```

### 1.4 Choosing a time for day `d`

```
1. events   ← all events for day-of-week d within 120 days
2. positive ← Σ typeWeight×recencyWeight over events with typeWeight > 0
3. if positive < 5.0:
       fall back to the GLOBAL profile (all seven days pooled)
       if the global positive mass is also < 5.0:  return 22:00, confidence 0
4. clamp the candidate range to slots 16…46  (08:00 … 23:00, D-28)
5. best ← argmax smoothed[s] over that range;  ties → the earlier slot (deterministic)
6. confidence ← min(1.0, positive / 20.0)
7. hysteresis:
       if a previous learned time exists for d
          and smoothed[best] < 1.2 × smoothed[previousSlot]:      keep the previous slot
       else move toward best by at most 3 slots (90 min) per recomputation
8. return slotStartMinute(best)
```

**Why step 7.** Without hysteresis the schedule jitters by ±30 min every day as single events shift the
argmax, which reads to the user as randomness and destroys any sense that the app has a routine. The
1.2× margin means a genuinely better time still wins, just not instantly.

**Why the 5.0 threshold.** Roughly two taps. Below that the "learned" time is noise, and the specified
22:00 default is a better answer than a confident wrong one.

**Recomputation** runs daily at 03:00 local (a `PeriodicWorkRequest`, not an exact alarm — D-24) and
writes all seven rows of `learned_schedule`.

### 1.5 The daily decision

```
scheduledMinute(day) =
    if settings.customScheduleEnabled:
        if settings.sameTimeEveryDay: settings.customTimeAll
        else:                         settings.customTimeFor(day)
    else: learnedSchedule[day].minuteOfDay ?: 1320       // 22:00 cold start

at scheduledMinute(today) ± 15 min tolerance:
    if lastNotifiedDate == today                 -> skip     # once-a-day cap, checked first
    if a sync run is active                      -> skip
    if pendingCount == 0                         -> skip     # nothing to ask about
    if pendingCount >= settings.threshold        -> NOTIFY   # any day
    if today == SUNDAY && createdThisWeekCount>0 -> NOTIFY   # weekly catch-up
    else                                         -> skip
```
`pendingCount`, `createdThisWeek` and the ISO-week definition are fixed in D-26 and
`03-DATA-MODEL.md §2.2`. The manual override in Settings bypasses learning entirely but **not** the
once-a-day cap or the zero-pending check — "do not annoy the user" outranks the schedule.

### 1.6 Worked example

A user taps the notification at 19:35 and 19:50 on weekdays, and opens the app around 11:00 on Saturday.

| Day | Positive mass | Outcome |
|---|---|---|
| Mon–Fri | 3.0 + 3.0 = 6.0 ≥ 5.0 | slot 39 (19:30) — the two taps smear into slots 39/39, and slot 39 wins |
| Sat | 1.0 (one direct open) < 5.0 | falls back to global; global is dominated by weekday evenings → 19:30 |
| Sun | 0 | global → 19:30 |

After three Saturdays the Saturday mass reaches 3 × 1.0 × decay ≈ 2.8 — still below 5.0. This is
intended: one weekly app-open is weak evidence, and the requirement's promise of per-day analysis is
kept without acting on noise. Weekend divergence appears once the user actually *syncs* on weekends
(weight 3.0), which is the behaviour worth learning.

---

## 2. Default selection rules on the sync list

> "Videos larger than 30 MB should automatically be selected with 'upload and remove from device',
> Images should always be selected only as 'upload'. All files should be pre selected."

```
selected = true                                   // always, for every file
mode = when {
    mediaType == IMAGE                 -> UPLOAD              // "always", no size exception
    mediaType == VIDEO && size > 30 MB -> UPLOAD_AND_DELETE
    mediaType == VIDEO                 -> UPLOAD
    mediaType == AUDIO                 -> UPLOAD              // unspecified; see below
}
```

**Decisions made here.**
- **30 MB = 30 × 1024 × 1024 = 31 457 280 bytes**, strictly greater than. Binary MB because that is
  what Android's file sizes and every file manager on the sink will report.
- **Images never get `UPLOAD_AND_DELETE` automatically**, regardless of size — the requirement says
  "always", and a 60 MB RAW/panorama is still an image. The user can set it manually.
- **Audio defaults to `UPLOAD`.** The requirement does not mention audio here. Voice recordings are
  small and often re-listened to on the phone, so the conservative default is to keep them.
- The user's manual change to a row is sticky: recomputing defaults on a later rescan must **not**
  overwrite a mode the user set. Track this with the mode having been written by the user
  (`media_item.mode` differing from the computed default is not sufficient — store the default
  alongside, or set a `modeUserSet` flag).

---

## 3. Media pipeline

### 3.1 On commit (synchronous, must be fast — the client is waiting)

```
re-hash staged file  →  compare (D-18)
  ├─ mismatch → delete staged, 409
  └─ match    → sanitize album (D-10) → build path (D-11) → resolve collision (D-13)
                → fsync → rename into library/ → insert blob+item+stats in ONE transaction
                → enqueue thumbnail (priority 10)
                → if video && !skipTranscode(probe): enqueue transcode (priority 100)
                → 201
```
Nothing that touches ffmpeg happens on this path. The requirement is explicit that compression is
asynchronous, and thumbnail generation is deferred for the same reason.

### 3.2 Thumbnail job (D-31)

```
image        → scale longest edge to N, WebP q80
video        → seek to min(duration × 0.1, duration − 0.1) or 1.0s, one frame, then as image
audio        → extract attached picture; none → ErrNoThumbnail (a normal, non-failing outcome)
sizes        → 256 (grids) and 1024 (tap preview); both produced by one job
output       → .homesink/thumbs/<ab>/<hash>_<size>.webp, write-temp-then-rename
```

### 3.3 Transcode job (D-29, D-30)

```
probe → skip if (width ≤ 2560 AND bitsPerPixel < 0.12)      // already efficient, leave it alone
      → skip if free space < 10 GiB
encode → .homesink/work/<jobId>.tmp.mp4 with the D-29 flag set
verify → 1. ffprobe: ≥1 video stream, |durationOut − durationIn| ≤ 1.0s
         2. full decode: `ffmpeg -v error -i out -f null -` exits 0 AND stderr is empty
         3. size(out) < 0.90 × size(original)
         4. fsync(out)
         ANY failure → delete temp, keep original, mark job done (with reason), stop
replace → rename(temp, libraryPath)                          // atomic, same filesystem
        → unlink original if it had a different extension
        → blobs.stored_hash = sha256(new file);  original_replaced_at = now
        → blobs.hash UNCHANGED                               // invariant S4 / D-08
        → insert blob_variants(hash, 'transcode', …)
```

`bitsPerPixel = bitrate / (width × height × frameRate)`. The 0.12 cut-off sits above a typical phone
H.264 capture (~0.2) and below an already-compressed H.265 file (~0.06), so first uploads transcode and
re-uploads of Homesink's own output do not.

**Gate 2 is the one that matters.** ffmpeg exits 0 on outputs that are truncated or contain corrupt
frames more often than is comfortable. A full decode pass costs roughly 5 % of the encode time and is
the only check that actually proves the file is playable — which is what makes it safe to delete the
original in a configuration where the phone copy is also being deleted.
