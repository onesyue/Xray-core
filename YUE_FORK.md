# Yue native fork ledger

This is the source fork consumed by `yue-node`; its immutable consumer pin and
deployed revision are owned by that repository and the deployment pipeline.

## Baseline

- Canonical repository: `https://github.com/XTLS/Xray-core`
- Latest canonical stable release (checked 2026-09-14):
  [`v26.3.27`](https://github.com/XTLS/Xray-core/releases/tag/v26.3.27).
- Latest canonical prerelease (checked 2026-09-14):
  [`v26.9.9`](https://github.com/XTLS/Xray-core/releases/tag/v26.9.9).
- Exact base commit: `c412e77a9b712082ac9ebf27fa793951cb5a7d85` — `upstream/main`,
  rebased 2026-09-14 (tags `v26.9.14-yue.1`, `v26.9.14-yue.2`, `v26.9.16-yue.1`). It is `v26.9.9` plus two commits (`ccb69ea5` Windows
  `readv` fix, `c412e77a` TUN inbound UDP destinations), neither of which is
  reachable from the Linux VLESS role.
- Previous bases: `cd4ce973e9f6ef3a7acf9a7030927b4143f9ea47` (`upstream/main`,
  2026-09-04, tags `v26.9.4-yue.1` / `v26.9.11-yue.1`);
  `5ca6f4b7d4dc20a881d4330e498892697627ec0c` (= tag `v26.7.28`).

The base requires the Go 1.27 line (`fd2ca748`); every builder in this
fork's workflows and Dockerfiles is pinned to 1.27.1.

### What the 2026-09-14 rebase brought in (`cd4ce973..c412e77a`, 17 commits)

| Upstream commit | Why it matters to this fleet |
|---|---|
| `6ce8dc53` / #6723 | Buffered writes crossing the remaining buffer capacity — previously carried here as cherry-pick `9859d535`, now dropped as a duplicate (identical patch-id); `TestBufferedWriterCrossesPartialBuffer` stays |
| `cecc88f4` | grpc-go 1.83.2 — previously carried here as `b2cada76`, dropped as a duplicate |
| `eef6e63b` | XHTTP client `WaitReadCloser` data race |
| `3e2f040c`, `c26d2eda` | Direct/Freedom outbound compatibility and `sockopt.dialerProxy` handling |
| `a1bf968b` | VLESS config `validateOutboundTransportSecurity()` |
| `18a1b504` | Finalmask: `udpHop` becomes a UDP mask; `QuicParams.udp_hop` / `UdpHop` / `ProxyConfig` messages removed from `transport/internet/config.proto` and its fields renumbered |
| `de2caf3c`, `01a034be` | Hysteria Unix masquerade socket path; Blackhole custom response |
| `fd2ca748`, `c037ccd9` | Go 1.27.x; `infra/vformat` now imports gofumpt as a library instead of `go install`ing it |
| `47cfe999` | `github.com/xtls/reality` bump — **deliberately not adopted, see below** |

Zero of the 17 commits touch `proxy/vless/` or `transport/internet/reality/`
(verified with `git log cd4ce973..c412e77a -- proxy/vless transport/internet/reality`).
The REALITY behaviour change lives entirely in the dependency bump.

### Deliberately held back: `github.com/xtls/reality` stays below `8cdf7bf9`

Held at `20260908045812-e1986a4d31ca` since `v26.9.16-yue.1` (previously
`20260322125925-9234c772ba8f`). Upstream `main` between the two is exactly
`9234c772 → 393f8de3 → e1986a4d → 8cdf7bf9 → 5dabb073`, so the hold now takes
the two fixes that sit before the MLKEM enforcement and nothing else — see
"v26.9.16-yue.1" below. The classical-hello measurement that follows was taken
on `20260322`; `reality_yue_keyshare_test.go` re-proves it on `e1986a4d`.

Upstream `47cfe999` moves the module to `20260908062103-8cdf7bf9c7f0`, which
includes "REALITY protocol: Reject outdated/strange Client Hello that doesn't
have X25519MLKEM768 before optional X25519". Measured 2026-09-14 with this
fork's own `UClient` against a local REALITY server on each revision:

| Fingerprint preset | 20260322 (kept) | 20260908 (upstream) |
|---|---|---|
| `chrome`, `firefox`, `safari` (auto = modern hello) | authenticated | authenticated |
| `hellochrome_120`, `hellofirefox_120`, `ios`, `edge`, `random` | authenticated | **rejected — falls through to the camouflage dest** |

mihomo (YueLink's core) strips X25519MLKEM768 from its hello unless the proxy
sets `support-x25519mlkem768: true`, and the panel's subscription templates do
not emit that key, so the upstream revision would lock every default-configured
YueLink/Clash-family client out of every REALITY node.
`transport/internet/reality/reality_yue_keyshare_test.go` pins the kept
behaviour and goes red on a silent re-bump. Cost of holding back, since
`v26.9.16-yue.1`: only upstream `5dabb073` (Go 1.27 sync) — the 17 KiB target
record buffer and the `DetectPostHandshakeRecordsLens` panic/leak/race fixes
are now taken. Retire the hold once the subscription
templates emit the MLKEM flag for every mihomo-family client, YueLink ships a
core that sends X25519MLKEM768 first, and a real-client canary passes.

### Previous rebase: 2026-09-04 onto `cd4ce973` (history, kept for the audit chain)

The base is the 2026-09-04 `main` snapshot, **not** the `v26.7.28` tag. At that
point it contained 33 commits beyond that prerelease. The rebase was taken for a
specific list of production-reachable stability fixes, not for features:

| Upstream commit | Why it matters to this fleet |
|---|---|
| `598bde74` | Router: data race in the API path |
| `77f98eba` | XHTTP client: race condition plus a data race |
| `dffc7ada` | XHTTP client: `Request.GetBody()` so h2 can replay after GOAWAY |
| `8ee131cb` | HTTP inbound: potential panic in `readResponseAndHandle100Continue()` |
| `cd4ce973` | WebSocket client: panic before real dialing in `delayDialConn` |
| `f124daf5` | Observatory: 100% CPU when no outbound matches `subjectSelector` |
| `d9c54026` | Sniffing: QUICv2 support |
| `540b9070` | Transport: bind the UDP outbound socket in the destination family |

### Selected upstream backports after that snapshot (superseded on 2026-09-14: #6723 is now in the base)

- [`6ce8dc53` / #6723](https://github.com/XTLS/Xray-core/commit/6ce8dc53e79842af71f0b4a360cb65d9eaa1e8f7):
  preserve buffered writes crossing the remaining buffer capacity. The exact
  upstream patch treats `ErrBufferFull` as a partial write, flushes, and
  continues; `TestBufferedWriterCrossesPartialBuffer` verifies byte-for-byte
  output after an already partly filled buffer receives several buffers of
  payload. This does not pull the prerelease's Go/REALITY migration.

The original snapshot comparison below applies before this selected backport.

Zero of those 33 commits touch `proxy/vless/` or
`transport/internet/reality/` — verified by tree hash — so REALITY and the
VLESS encryption surface are unchanged by the move.

The Yue commit applied onto `main` with **no conflicts**. The only conflict in
the whole rebase was our own `61ad1638` (grpc 1.82.1 → 1.83.1), which upstream
had already done in `5fe6d621`; it was skipped and superseded by the bump to
1.83.2.

### How to rebase next time

The history carries "reconnect" merges (`-s ours`, tree identical to the
rebased line) so that `codex/xray-main-yue` and `master` fast-forward without
force pushes. A plain `git rebase upstream/main` therefore also tries to replay
the pre-rebase line through the merge's second parent; only the first-parent
line is the fork. Use:

```sh
git rebase -i upstream/main   # then keep only: git rev-list --reverse --first-parent upstream/main..HEAD
```

or drop the second-parent commits from the todo list. Duplicated upstream
patches are skipped automatically by patch-id.

The branch is intentionally a source-level fork, not a transport fork. Its
retained changes are application-neutral seams or correctness fixes exercised
by the production VLESS role:

1. A safe config-creator replacement seam for decorating the dispatcher.
2. Embedder-owned per-user counters on buffered and raw-splice paths, including
   wrapper discovery and bounded in-flight splice accounting.
3. Listener drain APIs that stop accepting while retaining active sessions and
   protocol state until final close.
4. `yue_profile_vless` dependency boundaries that exclude the QUIC sniffer and
   Hysteria listener wiring from a VLESS-only build.
5. Immutable, synchronized TLS certificate reload snapshots.
6. Vision padding bounds that preserve a full payload instead of panicking or
   silently truncating it.
7. Paced splice: a `dispatcher.SplicePacer` seam plus `Inbound.RequiresSplicePacing`
   so a rate-capped credential keeps the raw kernel splice path and is charged
   per copied chunk instead of being pushed onto userspace AEAD (see below).
8. Geodata matcher caches keyed on the resolved asset path, so several embedded
   instances with different `xray.location.asset` directories never share a
   `geosite.dat:CODE` / `geoip.dat:CODE` entry (see below).

### v26.9.16-yue.4 (2026-09-22): REALITY Conn input buffers back to the Go 1.27.1 layout

`v0.0.0-yue.2` crashed production: yue-node `743c0876` (built on
`v26.9.16-yue.3`) killed the canary Reality slot with `fatal error: fault`
about 10s after start and the roll aborted. Upstream REALITY #30 had synced
`Conn` *ahead* of the Go 1.27.1 standard library: `rawInput` and `hand` became
`*bytes.Buffer` backed by `rawInputPool` / `handPool` (plus `smallInput`,
`maxIdleInputCap`, `handBuf` / `handLen` / `releaseHand`). VLESS Vision
(`proxy/vless/inbound` and `outbound`) looks up `input` / `rawInput` by
reflect and casts `p + Offset` to `*bytes.Reader` / `*bytes.Buffer` through
`unsafe.Pointer`, so it read a pointer word as a `bytes.Buffer`. Upstream
Xray-core never adopted #30 (it still pins reality `8cdf7bf9`).

`replace` now points at `github.com/onesyue/REALITY v0.0.0-yue.3`
(commit `7bac04da`, signed, GitHub-verified): `yue.2` with
those `Conn` fields and `readFromUntil` restored field-for-field to go1.27.1
`crypto/tls/conn.go` (`rawInput bytes.Buffer`, `input bytes.Reader`,
`hand bytes.Buffer`, no pools). The rest of #30 and the classical key-share
revert are unchanged. `v0.0.0-yue.2` must never be consumed again.

Two tests close the gap that let it ship:

- `proxy/vless/xtls_unsafe_layout_test.go` asserts `input` is `bytes.Reader`
  and `rawInput` is `bytes.Buffer` (by value, and present) on every type the
  unsafe cast targets: `reality.Conn`, `crypto/tls.Conn`, `utls.Conn`,
  `encryption.CommonConn`. Fails on `yue.2`.
- `testing/scenarios/vless_reality_vision_inprocess_test.go` runs a VLESS +
  `xtls-rprx-vision` + REALITY server and client in-process against a local
  TLS 1.3 REALITY dest (no Internet), proxies TLS 1.3 echo traffic so Vision
  really switches to direct copy (asserted by counting `CopyRawConn` log
  records, >= 1 per connection) and drains `input` / `rawInput` on both sides.
  Passes on `yue.3`; on `yue.2` every tunnelled connection hangs until the
  deadline and the test fails.

### v26.9.16-yue.3 (2026-09-21): REALITY takes upstream's Go 1.27 TLS sync via an onesyue/REALITY fork

The hold below `8cdf7bf9` also held back upstream REALITY `3c98159` (#30,
"Sync upstream Go 1.27+"), which brings the crypto/tls security fixes the
vendored copy lacked (e.g. rejecting a read-traffic-secret change while
handshake bytes are still buffered, and the KeyUpdate ordering). #30 sits after
the MLKEM enforcement, so it cannot be taken as an upstream revision.

`go.mod` now requires upstream `v0.0.0-20260921001439-3c98159dee38` and
replaces it with `github.com/onesyue/REALITY v0.0.0-yue.2` (commit
`08e997a6`, signed tag): upstream `3c98159` plus exactly one hunk in `tls.go`
that restores the pre-`8cdf7bf9` key-share selection, byte-identical to
`e1986a4d` (prefer a classical X25519 share, else the X25519 half of
X25519MLKEM768). `reality_yue_keyshare_test.go` authenticates `chrome`
(MLKEM+X25519) and the classical `hellochrome_120` / `hellofirefox_120` / `ios`
presets on it; against unmodified `3c98159` the three classical presets fail.
`reality_yue_record_detect_test.go` still passes (the #36 fixes are upstream
ancestors of `3c98159`). `v0.0.0-yue.1` of that fork exists but was signed with
an unverifiable tagger email; it is never consumed.

Also in this tag: upstream `dbb1ea30` (WireGuard netTun close with a write in
flight panicked the process) with a regression test, and a dependabot ignore
for `github.com/xtls/reality`.

### v26.9.16-yue.1 (2026-09-16): REALITY probe crash/leak/race fixes within the hold

Same base `c412e77a` and the same Yue patches as `v26.9.14-yue.2`. The only
change is `github.com/xtls/reality` `20260322125925-9234c772ba8f` →
`20260908045812-e1986a4d31ca`, an upstream commit — no hand patch, no reality
fork. It is a strict descendant of the old hold and a strict ancestor of the
MLKEM enforcement `8cdf7bf9`; the diff is two upstream commits in two files:

| Upstream commit | Change |
|---|---|
| `393f8de3` (#33) | Target TLS record buffer `8192` → `17 * 1024` (Xray-core #6356) |
| `e1986a4d` (#36) | `DetectPostHandshakeRecordsLens`: bound check before `data = data[length:]` (a destination record whose declared length exceeds the bytes read panicked the background probe that `transport/internet/tcp/hub.go` starts with a bare `go` — no recover, the whole process exits); `defer target.Close()` on both probe connections (leaked one fd per dest/SNI/ALPN on a failed handshake); CCS alert reader no longer assigns `Write`'s named return value (data race) |

`transport/internet/reality/reality_yue_record_detect_test.go` pins all three:
run against `20260322` the truncated-record test panics
(`slice bounds out of range [16389:8]`), the probe-leak test sees 6 of 6
connections never closed, and the CCS test reports `DATA RACE` under `-race`;
all pass on `e1986a4d`. `reality_yue_keyshare_test.go` still authenticates every
classical-X25519 preset.

### v26.9.14-yue.2 (2026-09-14): the last two vendor-only patches made native

Until this tag, `yue-node` carried two patches **only inside its `vendor/`
tree**, re-applied by hand on top of every fork revision and guarded by string
tests, because any `go mod vendor` regenerated them away silently
(compilation and tests stayed green; a capped user's download quietly fell back
to userspace AEAD, and two instances could serve each other's geodata). Both
are now fork commits with behavioural tests, on the same base `c412e77a`:

| Concern | Files | Tests |
|---|---|---|
| Paced splice — `SplicePacer` / `SplicePacerSource` / `FindSplicePacer`; `Inbound.RequiresSplicePacing`; `copyRawConnCounted` takes the pacer, bounds the chunk by `SpliceChunkBytes`, charges **after** each copied chunk and tears the connection down when a charge is refused; `CopyRawConnIfExist` falls back to the buffered path when the session demands pacing and no pacer is reachable | `app/dispatcher/stats.go`, `common/session/session.go`, `proxy/proxy.go` | `app/dispatcher/splice_pacer_yue_test.go`, `common/session/session_yue_test.go`, `proxy/proxy_splice_test.go` (per-chunk charging, chunk clamp, fail-closed on refusal), `proxy/proxy_splice_linux_test.go` (real TCP: paced splice stays zero-copy, no-pacer falls back, uncapped keeps splice, refusal tears down) |
| Asset-scoped geodata cache keys — `buildDomainRulesKey`, `CompactDomainMatcherFactory.getOrCreateFrom` and `buildGeoIPRulesKey` key on `platform.GetAssetLocation(file)` | `common/geodata/domain_matcher.go`, `common/geodata/ip_matcher.go` | `common/geodata/asset_scoped_cache_yue_test.go` (Mph, Compact and IPSet factories each built twice from two directories holding the same code; key stability and distinctness) |

The compact (ios/android) factory's per-rule cache had the same bare
`file:code` key and is fixed here too; the vendor-only patch had covered only
the Mph and IPSet caches the Linux fleet actually uses.

Every listed test was mutation-checked: reverting the key change turns all
four geodata tests red, and disabling the `RequiresSplicePacing` branch turns
`TestCopyRawConnIfExistFallsBackToBufferedWhenNoPacerIsReachable` red on Linux.
The Linux tests run in `golang:1.27.1` (`go test -race ./proxy …`); on darwin
they compile out, so a green darwin run proves nothing about the splice path.

The pacer itself (token bucket, chunk size, wait budget, metrics) stays in
`yue-node`; the fork only owns the seam and the fail-closed rule.

## Changes deliberately not replayed

The old `yue-node-native` branch mixed these hooks with transport patches. The
transport changes are absent here because `v26.7.28` already contains their
equivalents:

| Old fork concern | Canonical equivalent in or before `v26.7.28` |
| --- | --- |
| Trusted `X-Forwarded-For` for XHTTP, WebSocket, HTTPUpgrade and gRPC, including gRPC remote address propagation | `711aea4e` |
| HTTPUpgrade handshake deadline/header bound and transient accept retry | `c320e891` |
| QUIC Initial parser length bounds and malformed-packet panic fixes | `e5a9fb75`, strengthened by `8f15190c` |
| Destination-derived TLS server name for gRPC and Hysteria | `64fada32` |
| Accurate XHTTP/gRPC server local address | `4aba687d` |

No old gRPC, HTTPUpgrade, WebSocket, Hysteria or XHTTP-server source
modification is carried by this branch. The canonical QUIC parser is also
unchanged; only its role-specific build-tag wiring is split for the VLESS
profile boundary described above.

The old XHTTP `WaitReadCloser` ownership patch
`8367c408ca8529c026e0e729704e67587f133b09` is also deliberately absent. The
production `yue_profile_vless` artifact did not link that code, so an unrelated
XHTTP change is not justified in the minimum production fork.

The concrete bandwidth feature from old commits
`f479355399657c0ed07b3e8ac33e02f727094188` and
`bd62f984273490e29111c109e0c28024896e486e` is not part of Xray-core. It is
application policy and has moved into `yue-node` at `27d9a401`: the consumer
now owns `UserLimiter`, generation-safe publication, and the existing metrics.
The exact acceptance contract remains in
[`docs/yue-node-limiter-migration.md`](docs/yue-node-limiter-migration.md).

## Required gates before a consumer pin change

```sh
go test ./...
go test -race ./common/... ./app/dispatcher ./app/proxyman/inbound ./proxy ./transport/internet/tls
go test -tags yue_profile_vless ./...
go build ./...
go build -tags yue_profile_vless ./...
```

From the `yue-node` checkout, the VLESS dependency audit must additionally run
its canonical `scripts/check-profile-deps.sh` gate (which targets
`./cmd/yue-node`, not every package in the Xray module) and prove that the
consumer dependency graph contains none of these packages:

- `github.com/apernet/quic-go`
- `github.com/xtls/xray-core/common/protocol/quic`
- `github.com/xtls/xray-core/proxy/hysteria`
- `github.com/xtls/xray-core/transport/internet/hysteria`

Do not change the `yue-node` pin until its migration checklist, both profile
test suites, limiter overload tests, and a VLESS canary all pass.
