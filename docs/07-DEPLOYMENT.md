# Homesink — Deployment & Distribution

> The requirements ask to "analyze different approaches for the server setup on a Linux Mint machine or
> a Ubuntu server and ask for further clarification where needed." §3 is that analysis. §7 is what still
> needs answering.

## 1. Target hosts

Linux Mint 21/22 and Ubuntu 22.04/24.04 share a package base, so one path covers both. The relevant
difference is not the distro but the **role**:

| | Mint desktop | Ubuntu server |
|---|---|---|
| Session | Graphical, user logs in and out | Headless, no login |
| Consequence | A rootless user service **stops at logout** unless lingering is enabled | Lingering must be enabled from the start |
| Fix | `loginctl enable-linger $USER` — required on both, for different reasons |
| Storage | External USB drive, often auto-mounted under `/media/$USER/…` | Fixed mount in `/etc/fstab`, usually `/srv` or `/mnt` |
| Consequence | Auto-mount paths change between reboots and after replugging | Stable |
| Fix | Mount by UUID in `/etc/fstab` with `nofail`; never use the auto-mount path |

Both fixes are in `install.sh`, and both are the actual causes of "it stopped working after a reboot".

## 2. Chosen setup — rootless Podman + systemd quadlet (D-35)

Podman ships in Ubuntu/Mint repos, runs rootless, and integrates with systemd natively — no extra
daemon, no socket exposure, and `podman auto-update` gives **automatic rollback**, which is the property
that made it the choice.

`deploy/homesink.container` (a quadlet unit → `~/.config/containers/systemd/`):

```ini
[Unit]
Description=Homesink media sink
After=network-online.target
Wants=network-online.target

[Container]
Image=ghcr.io/<owner>/homesink:latest
AutoUpdate=registry
PublishPort=8443:8443
Volume=/srv/homesink:/data:Z
Environment=HOMESINK_DATA=/data
Environment=TZ=Europe/Berlin
EnvironmentFile=%h/.config/homesink/homesink.env
HealthCmd=/homesinkd healthcheck
HealthInterval=30s
HealthRetries=3
HealthStartPeriod=20s
Notify=healthy

[Service]
Restart=always
TimeoutStartSec=120

[Install]
WantedBy=default.target
```

`Notify=healthy` is what makes rollback work: systemd does not consider the unit started until
`/healthz` passes, so `podman auto-update` sees a failed start and restores the previous image
automatically. This is why `WP-B11` requires `/healthz` to actually check the DB and the data
directory rather than returning a constant 200 — a health check that cannot fail cannot roll back.

Enable auto-update:
```bash
systemctl --user enable --now podman-auto-update.timer
```

### 2.1 Container image

`FROM alpine` (not `scratch`) because ffmpeg is a hard runtime dependency (D-31). Multi-stage:
Go builder → `alpine:3.20` + `ffmpeg` + `ca-certificates` + `tzdata`. Non-root user, single
`/data` volume, `HEALTHCHECK`, image target <150 MB. `CGO_ENABLED=0` with
`-ldflags "-s -w -X …buildinfo.Version=…"` — this is why `WP-B2` mandates the pure-Go SQLite driver.

## 3. Alternatives considered

| Approach | Self-update | Isolation | Rollback | Verdict |
|---|---|---|---|---|
| **Podman quadlet + auto-update** | `podman auto-update.timer`, daily | rootless container | **automatic, on failed health check** | **Chosen.** Best rollback story, no daemon, native to both distros. |
| Docker Compose + Watchtower | Watchtower polls the registry | container, but Watchtower needs `/var/run/docker.sock` | none — a broken image stays up | Most familiar, and `compose.yaml` ships for people who want it. The socket is root-equivalent on the host, and no rollback is a real cost for an unattended box. |
| Docker + in-app binary self-update | app downloads a signed binary into the volume and re-execs | container | manual (keep the previous binary) | Works without a registry, but the app updates *itself* while ffmpeg and the base image stay pinned — the two drift apart and the drift is invisible until something breaks. |
| Bare systemd service, no container | app self-update or `apt` | none | manual | Simplest to debug and lowest overhead, and genuinely reasonable on a Mint desktop. Rejected as the default because it depends on the distro's ffmpeg version, which is exactly the dependency D-29/D-30 are most sensitive to. |
| Snap / Flatpak | store-managed | strong | automatic | Rejected: strict confinement fights arbitrary external-drive paths, which is the whole point of the app. |

**If you would rather use Docker**, `deploy/compose.yaml` ships with a label-scoped Watchtower. Accept
that you lose rollback and expose the socket; nothing else changes.

## 4. Install flow (`deploy/install.sh`)

Idempotent, re-runnable, and it explains rather than assumes:

```
1. Detect distro/version; require podman ≥ 4.4 (quadlet support) or offer apt install
2. loginctl enable-linger $USER                        ← the reboot/logout fix from §1
3. Prompt for the data directory; verify it is a mount point, writable, and has ≥50 GB free
4. Warn if it is under /media/$USER (auto-mount) and offer to write an /etc/fstab UUID entry
5. Write ~/.config/homesink/homesink.env from the answers
6. Install the quadlet unit; systemctl --user daemon-reload; enable --now homesink
7. Enable podman-auto-update.timer
8. Wait for /healthz, then print the pairing code and the LAN URL
9. Offer to open the firewall (ufw allow 8443/tcp) — asking, never silently
```

The script prints the pairing code at the end because the very next thing the user does is install the
APK and pair, and making them hunt for `homesinkd pair` is the first place onboarding breaks.

## 5. Client distribution

The APK reaches the phone in two hops, and only the first is manual:

1. **First install:** browse to `http://<server>:8443/` on the phone → a minimal German landing page →
   download → Android asks to allow installs from the browser → install. The landing page must state the
   SHA-256 so a careful user can verify it.
2. **Every update after that:** in-app banner (D-34), verified by SHA-256 against `/v1/app/latest`.

**Signing.** One keystore for the life of the project. Android identifies an app by its signing key —
losing the keystore means every user must uninstall (losing app data) before installing again. Keep it
out of the repo, in CI secrets plus one offline backup, and say so in `client/README.md`.

## 6. Backup and recovery

| What | How | Why |
|---|---|---|
| `library/` | The user's own backup (second drive, rsync). Homesink does not back it up. | It is plain files in a plain tree — that is the point of the layout |
| `.homesink/db/` | Nightly `VACUUM INTO`, keep 7 (D-36) | Losing it loses albums, dedupe state, pairings |
| TLS key | Inside the DB, so covered above | Losing it un-pairs every device (D-02) |
| Keystore | Offline, outside the repo | §5 |

**Recovery from a lost DB:** the library files are intact, so `homesinkd fsck --rebuild` re-imports the
tree by walking `Album/Year/Month/` and re-hashing. Pairings and devices are not recoverable — every
phone re-pairs. This is documented rather than automated because it should be rare and the user should
know what they are getting back.

## 7. ❓ Needed before release

1. **GitHub owner/repo and the container registry path** (`ghcr.io/<owner>/homesink`) — hard-blocks
   `WP-B11` and `WP-B12`, and appears in the quadlet unit above.
2. **Whether releases should be public.** A public GHCR image means no registry auth on the home server
   (simpler); a private one needs a pull secret in the quadlet.
3. **Default data path** — `/srv/homesink` (server convention) is assumed above; a Mint desktop user
   with a USB drive will more likely want a `/mnt/homesink` fstab entry. `install.sh` asks either way.
