# Yue native fork ledger

This branch is a non-production candidate. It does not move any tag, rewrite an
older branch, or change the pin consumed by `yue-node`.

## Baseline

- Canonical repository: `https://github.com/XTLS/Xray-core`
- Canonical tag: `v26.7.28`
- Exact base commit: `5ca6f4b7d4dc20a881d4330e498892697627ec0c`
- Candidate branch: `native/v26.7.28-yue`

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
application policy and must move into `yue-node` before that repository changes
its pin. The exact migration gate is in
[`docs/yue-node-limiter-migration.md`](docs/yue-node-limiter-migration.md).

## Required gates before a consumer pin change

```sh
go test ./...
go test -race ./common/... ./app/dispatcher ./app/proxyman/inbound ./proxy ./transport/internet/tls
go test -tags yue_profile_vless ./...
go build ./...
go build -tags yue_profile_vless ./...
```

The VLESS dependency audit must additionally prove that `go list -deps -tags
yue_profile_vless` contains none of these packages:

- `github.com/apernet/quic-go`
- `github.com/xtls/xray-core/common/protocol/quic`
- `github.com/xtls/xray-core/proxy/hysteria`
- `github.com/xtls/xray-core/transport/internet/hysteria`

Do not change the `yue-node` pin until its migration checklist, both profile
test suites, limiter overload tests, and a VLESS canary all pass.
