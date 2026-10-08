# Multi-user frontend contract and implementation plan

This frontend subtask is authorized against the API contracts in the request. It owns only dashboard authentication, account, administrator and enrollment pages, their shared assets/tests, and app/index/service-worker integration. No backend, deployment, commits, or unrelated edits belong to this subtask.

## Behavior

- `/auth` defaults to login; `/auth/register`, `/auth/verify`, `/auth/setup` and `/auth/recovery` share one static shell. Registration sends exactly email, password and confirm_password. A pending cookie never opens the dashboard. Both challenge and mandatory Authenticator setup finish through auth/verify. QR images come from auth/qr; secrets and recovery codes live only in the DOM/memory and recovery codes require acknowledgment before navigation.
- `/account` displays identity, owned device metadata and login sessions, changes password, rotates Authenticator/recovery codes, revokes other sessions and logs out. Destructive actions require explicit confirmation. `/account#add-device` opens server-generated confirmation codes/links and explicitly notes that the actual Mac client installer is a later phase.
- `/admin` requires an authenticated admin and displays only user/device/event metadata, searchable paginated lists, user details, disable/enable, session revocation and device removal. It never reads device content, messages or files.
- `/enroll/confirm?code=` authenticates before displaying a confirmation action and sends precisely `{code}` to enrollment/confirm. It preserves its local return URL through authentication and never confirms automatically.
- A shared fetch client supplies same-origin credentials, no-store for all APIs, the me CSRF token on authenticated writes, error messages, and 401 cleanup/redirect. Pending authentication failures remain on the form. XHR uploads use the same token and expiry handling. No automatic mutation retries.
- Dashboard private preferences/read state/pool snapshots use a user ID namespace. Legacy unscoped private keys are discarded. Logout/expiry clears the current namespace in both storages, stops further writes, hides sensitive DOM and signals other tabs. Theme is public and stays global.
- Dashboard initialization (including preview routing) awaits me before starting polling or loading data. Device discovery consumes the owned `/api/devices` roster. Account/admin/add-device entries appear in both menus; admin is hidden from ordinary users.
- The service worker bypasses exact and nested auth/account/admin/enroll/API paths and device proxy paths. Authentication redirects must never enter the cached dashboard shell.

## Failure semantics and tests

Forms disable duplicate submission and retain a readable inline error on network/validation/403 errors. 401 on full-cookie APIs removes private state and redirects to login; wrong pending credentials remain recoverable. Direct setup/verify links use the pending cookie; refresh uses the QR endpoint rather than persisted secrets. Backend enforces role, ownership, status, same Origin and CSRF; UI checks only improve navigation.

1. Write and run `node --test server/dashboard/auth.test.mjs` before implementing the shared client. Exercise actual requests, CSRF, pending failures, 401/late-response handling, user isolation and logout.
2. Write and run page tests before implementing shared DOM flows/shells. Exercise exact payloads, challenge/setup, recovery acknowledgment, account operations, admin metadata/pagination, enrollment return paths and errors using a minimal DOM harness.
3. Write failing integration tests before changing app/index/sw. Verify auth gating, owned device discovery, storage isolation, upload CSRF and worker cache boundaries, including redirected navigation.
4. Run the new tests, existing dashboard suite and `bash scripts/verify.sh`; report fresh output and remaining server routing/contracts needed. Preserve concurrent changes throughout.

## Parent integration requirements

账户添加设备区提供 `/enroll/mac-bundle.tar.gz` 完整安装包与 `/enroll/dist/fleet-agent-darwin-{arm64,amd64}` 下载入口。首次使用完整包运行 `mac/install.sh`，准备支持文件并验证正式签名 agent 后，由 agent 发起配对；已有支持文件的客户端使用 `fleet-agent login`。用户在终端输入完整 HTTPS origin（含实际端口）、浏览器登录及 Authenticator 确认、回到终端核对 owner 并输入 y 后继续安装。网页只展示步骤与打开已生成短码的确认页，不调用 enrollment/start，不推断 owner。下载由 nginx 分发，入口存在不表示已发布支持新协议的签名公证产物；下载 404 或 capabilities 失败时必须停止，不回退旧版或跳过 TLS 校验。本地 Go 实例默认不分发正式客户端。

Serve all `/auth` and `/auth/*` variants from `auth.html`, account from `account.html`, admin from `admin.html`, and enrollment confirmation from `enroll.html`; preserve query strings. Make `/auth-client.js`, `/account.js`, `/account.css`, `/style.css` and the favicon available before authentication. Auth QR and all JSON APIs require `Cache-Control: no-store`. The HTTP-only cookie, 30-day lifetime, role/status/ownership checks, CSRF and same-Origin enforcement are backend responsibilities. Errors should carry a readable message (or error string). Security changes may return `login_required: true`; TOTP changes retain recovery codes until acknowledgment before reauthentication. Existing names/settings/automation and all `/mN/` routes must remain owner-scoped. Login verification accepts null/empty recovery_codes without rendering an empty-code acknowledgment screen; nonempty codes require acknowledgment. Account session and administrator event fields are rendered conservatively as metadata; no content endpoints are called by these pages.

## Real local browser UAT — 2026-10-01

Target: `http://127.0.0.1:7099`, using the parent's real isolated backend/database and Headscale on port 56693. No mock server or intercepted responses were used. Browser extension/IAB connections timed out; native Chrome accessibility control recovered and completed the UI workflow in a new, dedicated tab.

Verified through actual UI and Chrome DevTools:

- Registered a new synthetic account with exactly email/password/confirmation; reached mandatory Authenticator setup. Public auth-client.js returned 200.
- Setup loaded GET `/api/auth/qr`: network panel showed 200, PNG, approximately 1.9 kB. Refresh retained the pending setup flow and QR access. Locally computed TOTP verified successfully; setup returned ten recovery codes and required acknowledgment before opening the dashboard.
- Dashboard displayed the new identity and no owned devices. Account showed the current session; creation and expiry differed by 2,592,000 seconds (30 days). Server-only add-device instructions explicitly retained the next-phase Mac installer limitation.
- Account logout-others completed successfully through the real CSRF-protected write and displayed its success message. Logout returned to auth. Direct account navigation preserved `?next=%2Faccount`; password login reached challenge, TOTP returned directly to account without an empty recovery-code screen.
- Ordinary-user `/admin` access returned the expected 403. The local admin CLI promoted only the new synthetic UAT account. Administrator navigation, overview, real user search, user details, login audit metadata and device roster loaded. Existing protocol-fixture device records belong to the parent's HTTP smoke, not a frontend mock or a real Mac installer test.
- Backend correctly rejected self-disable with a readable inline error. Local maintenance reset then invalidated the UAT account's old password, Authenticator secret, recovery codes and sessions. A subsequent admin detail request triggered the frontend's real 401 cleanup and redirected to `/auth?next=%2Fadmin`.

An actual frontend autofill defect was found: an empty autocomplete value and three password forms lacking account identity. A regression test failed first, then account.js supplied valid autocomplete values and an invisible username hint outside the submitted payload. Registration still has exactly three fields. Dashboard suite after the fix: `node --test server/dashboard/*.test.mjs`, 256 passed, zero failed; dashboard diff check passed.

Successful auth/account/admin/dashboard pages had zero console messages with all log levels enabled; repaired account and admin also had zero DevTools Issues. Dashboard retained four non-blocking unassociated-label accessibility suggestions. Negative tests intentionally produced 403, 400 self-disable and 401 expiry responses. The backend's bare 403 error document also requested favicon.ico and received 404; no frontend JavaScript exception was observed. This does not claim that negative-test network errors or all accessibility suggestions are absent.

Screenshots (no QR, password, TOTP secret or recovery codes): `/private/tmp/macfleet-frontend-real-uat-20261001/register.png`, `account.png`, `account-console.png`, `admin.png`, `admin-overview.png`, `admin-console.png`, `dashboard.png`, `dashboard-console.png`, and `expired-session-console.png` in that directory. The temporary setup screenshot containing credentials was removed. Recovery-code output redaction failed once during the tool observation; the local maintenance reset explicitly invalidated that entire batch and all old credentials. No usable UAT credential remains, and no credentials were written to repository files.

No backend source changes, commits or deployment were made by this frontend subtask. Parent requirements remain public auth assets/all auth subroutes/query preservation and the documented JSON/CSRF/no-store contracts; the tested verification route is `/api/auth/verify`. Live Mac installation/enrollment, password/TOTP rotation, multi-tab storage inspection and multi-page pagination were not part of this final real-browser pass; their frontend contract tests do not replace future end-to-end UAT. Parent review subsequently fixed raw Unix timestamp rendering with a failing regression test; account/admin/device dates now use readable local time and unknown timestamps show a dash. The screenshots predate that date-formatting fix.
