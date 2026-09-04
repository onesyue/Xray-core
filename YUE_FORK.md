# Yue native fork ledger

This branch is a non-production candidate. It does not move any tag, rewrite an
older branch, or change the pin consumed by `yue-node`.

## Baseline

- Canonical repository: `https://github.com/XTLS/Xray-core`
- Latest canonical **release**: `v26.7.28` (still the newest tag upstream has cut)
- Exact base commit: `cd4ce973e9f6ef3a7acf9a7030927b4143f9ea47` — `upstream/main`, rebased 2026-09-04
- Previous base: `5ca6f4b7d4dc20a881d4330e498892697627ec0c` (= tag `v26.7.28`)

🚨 The base is now `main`, **not** the `v26.7.28` tag, and the two are not the
same thing: upstream has 33 unreleased commits on top of that tag and has cut no
release since 2026-07-28. Anyone reading only the tag will conclude we are
current when the base has in fact moved past it. The rebase was taken for a
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

Zero of those 33 commits touch `proxy/vless/` or
`transport/internet/reality/` — verified by tree hash — so REALITY and the
VLESS encryption surface are unchanged by the move.

The Yue commit applied onto `main` with **no conflicts**. The only conflict in
the whole rebase was our own `61ad1638` (grpc 1.82.1 → 1.83.1), which upstream
had already done in `5fe6d621`; it was skipped and superseded by the bump to
1.83.2.

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
