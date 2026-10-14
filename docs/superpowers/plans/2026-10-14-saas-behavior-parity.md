# Execution ledger

Base: 390d2bdd39e2fae0a5b69ec4bc41670b25934a58.
Specification: ../specs/2026-10-14-saas-behavior-parity.md

Independent ownership:
- Auth: items 1–4, 11–12, 27; auth/account/theme frontend and authentication server.
- Server: items 19–23 plus server side 7/26; automation isolation/indexing, networking, device authorization.
- Agent runtime: items 5–7, 26; daemon/service resilience and device leases.
- Native: items 8–10, 17–18, 24–25; Swift management/migration/updates and maintenance gate.
- Chat/dashboard: items 13–16 and resource preferences in 28; main parity and composition.
- Files: item 28 filesystem access, encoding, oversized preview/download.
- Root: contracts, shared-file integration, tests, cleanup, commits/deployment evidence.

Workers must not commit, deploy, replace live services or revert another worker's edits. Coordinate shared files with root. Before implementation inspect current source because other chats may have already fixed a finding.

Completion and red/green evidence will be appended during integration.

## Integration evidence — 2026-10-15

All six business owners completed implementation and cross-review. Existing concurrent toolbar/settings commit `0e4d356` was preserved. No worker committed or changed live services.

Red/green cases included security credentials incorrectly logging out; BFCache pending challenge/recovery acknowledgement; rotated-session old401 and stale palette races; relative lease wall-clock skew; missing-node permanent revocation; slow network blocking unrelated requests;16-second cold start timeout; corrupt automation store isolation; unavailable pre-send job pause; native retry and component failures; listener ownership; legacy queue migration;5MiB CSS preview vs download;UTF16 literal replacement character; project/model parity; explicit idle0; live tmux task preservation.

Cross-review fixes: verify listener PID before proxy publication; reject maintenance without disconnecting sockets; bind palette responses to request identity; keep recovery acknowledgement on BFCache; discard old-generation401 after credential rotation; recheck stream expiry after renewal; only compare ACL-relevant state during network reconciliation.

Final project verification: `bash scripts/verify.sh` passed (`/tmp/fleet-parity-verify-final.log`), including Go agent, native TLS build-tag tests, Go server, dashboard, all122 Swift tests, and all shell/deployment guards. Dashboard full glob run455/455 passed. Targeted Go race tests passed for runtime/maintenance/auth-network/automation paths. `git diff --check` passed.

Boundaries: Mac artifacts are not signed/released. Offline Web send intent requires keeping the page open and says so before enqueue; an ambiguous network-send result is not replayed. Native wake/network notification accelerates retries while settings is open; daemon always retries independently. Two distinct populated old/new delivery journals are preserved with actionable recovery instead of unsafe automatic merge. Browser and real-device acceptance are separate from isolated tests.

## Web/server delivery evidence

Deployed immutable source revision `85fa8d7cbf4430bf76adefa1ef88faee551fda63` on2026-10-15. Server binary built from that commit's archive for linux/amd64; release manifest144 files verified before activation. Prior release and private state/config backup retained for rollback. No Mac distribution artifact changed.

Public `/healthz`, `/readyz`, `/auth` returned200; unauthenticated private `/api/devices` returned401. Four gateway services active. Public auth/theme/account/style assets matched the package; private app/preview assets correctly redirected unauthenticated access, and their server-side SHA256 matched the local release. Server binary SHA256 `ffe0ba7880a4f2c38e881f3f08ef4539f3b1394361418c7dbcc009e216e531d6`.

Real Chrome loaded the updated login page with no captured console errors. Browser was not logged in; no production password, TOTP or recovery credential was entered or changed. Authenticated flow assertions are isolated behavioral tests, not claimed as a production end-to-end login test.
