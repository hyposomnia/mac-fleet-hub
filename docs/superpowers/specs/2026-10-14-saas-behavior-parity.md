# SaaS behavior parity and recovery

Status: approved by user, 2026-10-14. Implements review items 1–28.

## Invariants

- SaaS ownership, CSRF, explicit revocation and credential boundaries remain enforced.
- Codex always uses the existing shared keeper/app-server and one physical writer. Never recover by starting isolated writers or killing an active Desktop turn.
- Web/server changes may be deployed to `ssh pm` after verification under the user's standing authorization. Mac source/tests only; no signing, production installation, launchd or Desktop replacement until requested.
- Preserve OS file permissions; remove gratuitous product path restrictions.
- Text preview remains limited to 5 MiB. Larger files show “文件过大，不支持预览” and an immediately usable download action. No large-text paging work.
- Remove superseded paths rather than keeping duplicate implementations. Keep compatibility only where existing deployed clients need it.

## Acceptance matrix

1. Incorrect security-action password/OTP remains inline; actual expired sessions redirect.
2. Browser history restoration resumes the correct pending/full authentication stage.
3. Authenticator replacement rotates current credentials, revokes other sessions and preserves recovery-code acknowledgement.
4. Palette caches/imports cannot cross account identities.
5. Native enrollment recovery retries automatically with bounded backoff and wake/network responsiveness.
6. Files/terminal component failures recover independently without taking down healthy agent functions; local port conflicts handled without manual edits.
7. Device authorization expiration tolerates wall-clock skew through relative monotonic leases.
8. Upgrade health checks local executable/runtime health independently of external network availability.
9. Update discovery remains usable when background management is unavailable.
10. Legacy migration preserves pending queue identity and avoids duplicate delivery.
11. Client IP derives only from trusted proxy forwarding.
12. Password character-count validation matches displayed rules and retains byte resource bounds.
13. DSH model catalog/selection and effort behavior match main.
14. New-session project selection matches main, with convenient defaults.
15. Offline composition preserves drafts and makes deliberate deferred sending explicit; never silently submits a draft.
16. Claude Desktop refresh default matches main without taking focus or interrupting input.
17. Shared Codex runtime remains invariant, reconnects without creating another writer.
18. Legacy assistant/proxy configuration migrates without silent loss, preserving shared mode.
19. One corrupt account automation store cannot disable other accounts.
20. API-key lookup avoids scanning/loading all users per request; queued jobs still recover at startup.
21. External network work does not hold broad server state locks; commits revalidate changed state.
22. Single missing-node snapshots do not permanently revoke devices; explicit revocation remains immediate.
23. Timeouts distinguish short health checks from cold startup and streaming, without retrying non-idempotent sends.
24. Keep supported Applications installation location; provide actionable one-step install/open guidance.
25. Idle browser/terminal connections do not block maintenance; protect active work and preserve shared server on agent-only updates.
26. Transient authorization outages receive a bounded configurable grace period for existing valid credentials; explicit revocation bypasses grace.
27. TOTP remains default; session lifetime, authentication limits/concurrency configurable; meaningful retry timing; audit pagination; sliding activity renewal with sensitive-action reauthentication.
28. OS-permitted file navigation, 5 MiB preview/download fallback, common text encodings; reduce arbitrary window/cache caps; support disabling idle connection reclamation without killing tasks.

## Validation

Each business owner records a failing behavior test before implementation and passing targeted tests afterward. Tests must exercise outcomes, not source-text mirrors. Root integration runs `bash scripts/verify.sh`, server Go tests and relevant dashboard/native tests. No test or fixture may modify production credentials, network, launchd or Desktop. Distinguish isolated tests from real production/browser verification and unsigned Mac source from released artifacts.
