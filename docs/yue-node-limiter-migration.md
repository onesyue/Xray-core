# `yue-node` limiter migration gate

This is a migration checklist only. This Xray-core change does not edit
`yue-node`, its vendored tree, module pin, images, or production deployment.

## Why the limiter moves

Per-user speed policy, queue caps and Prometheus metric names belong to the
embedding service. Xray-core retains only the general seams needed by an
embedder: dispatcher decoration, injected counters, transparent writer
unwrapping, raw-splice accounting and mux-safe reader control forwarding.

## Exact source migration

Before pinning this branch, make these changes in `yue-node` in one separately
reviewed change set:

1. Add a `yue-node`-owned limiter implementation under
   `internal/kernel/xray/` (suggested files: `ratelimit.go`,
   `ratelimit_metrics.go`, `ratelimit_test.go`). Move only these behaviors from
   the old fork's `features/bandwidth/bandwidth.go` and
   `app/dispatcher/ratelimit.go`:
   - a shared `UserLimiter` for both directions;
   - the immediate `AllowN` fast path;
   - per-user waiter cap `256`;
   - process waiter cap `512`;
   - cumulative wait deadline `2s`;
   - chunking by token-bucket burst;
   - fail-closed queue/deadline errors;
   - current/peak/reject/wait-time/waited-byte metrics;
   - reader/writer close, interrupt, `ReturnAnError` and `Recover` forwarding;
   - writer `UnwrapWriter()` so Xray's splice counter discovery remains valid.
2. Add `limiters map[string]*UserLimiter` to `LimitDispatcher`, protected by its
   existing policy mutex. Extend `admittedSession` with the exact limiter
   pointer selected from the same policy generation as credentials, counters,
   device limits and `policyGen`. Never look it up again after admission.
3. Replace the current Xray feature publication in
   `internal/kernel/xray/xray.go:configureBandwidthLimits` with an atomic
   `LimitDispatcher.ReplaceUserLimiters` publication. Reuse an existing
   `UserLimiter` when its underlying `*rate.Limiter` pointer is unchanged so a
   periodic snapshot cannot split one credential generation across independent
   waiter caps.
4. In `LimitDispatcher.Dispatch`, run admission first, call the inner
   dispatcher, then wrap the returned link's reader and writer with the
   admitted limiter. In `LimitDispatcher.DispatchLink`, wrap the supplied link
   before calling the inner dispatcher. Its reader wrapper must accept a plain
   `buf.Reader`; Xray's `WrapLink` adds timeout semantics around it. Both paths
   must set `session.Inbound.CanSpliceCopy = 3` before entering Xray whenever a
   limiter is present.
5. Preserve the existing transaction order in Add/Update/Remove user flows:
   publish counters + limiter + dispatcher policy before credential visibility;
   on failure, restore the complete prior generation before returning. A
   credential must never be briefly visible without its limiter.
6. Change `cmd/yue-node/observability_xray.go` to read the new local metrics
   snapshot. Keep the existing metric names and label values byte-for-byte so
   dashboards and alerts do not fork.
7. Remove every import of `github.com/xtls/xray-core/features/bandwidth` from:
   - `internal/kernel/xray/xray.go`
   - `internal/kernel/xray/dispatcher_test.go`
   - `internal/kernel/xray/lifecycle_test.go`
   - `internal/kernel/xray/xray_test.go`
   - `internal/kernel/xray/user_transaction_test.go`
   - `cmd/yue-node/observability_xray.go`
8. Update `go.mod`, `go.sum`, `vendor/modules.txt`, the vendored Xray tree and
   `fork-ledger.yaml` only after the source migration passes. The ledger must
   record this branch's final signed commit and the official base above; it
   must no longer claim a core-owned bandwidth feature.

## Mandatory regression matrix

- Unlimited traffic allocates no limiter timer and remains splice-eligible.
- Limited VLESS uplink and downlink both obey one shared bucket and force
  buffered copy before Vision can switch to splice.
- A full-size multi-buffer is chunked by burst without `WaitN` rejecting
  `n > burst`.
- The 257th per-user waiter and 513th process waiter fail closed and release
  their buffer.
- The 2-second cumulative deadline fails closed and decrements both waiter
  counters exactly once.
- Context cancellation is not counted as limiter overload.
- Unchanged policy snapshots reuse the limiter gate; changed credentials get a
  new generation and old connections retain the old gate.
- Add/update/remove rollback tests observe no credential-without-policy window.
- `Dispatch` and VLESS `DispatchLink` tests cover reader and writer directions.
- Mux/XUDP close passes through limiter, accounting and timeout reader wrappers.
- Existing Prometheus tests see unchanged metric names and monotonic counters.
- `make test`, both `yue_profile_hy2` and `yue_profile_vless` race suites,
  profile dependency checks, vulnerability checks and Linux release builds all
  pass before a canary.

Only after this matrix passes should a separate deployment change build a new
image and canary one VLESS node. This branch itself must not move production.
