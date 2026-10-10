# Fleet Hub 0.1.8+9 release and local installation

The complete official `scripts/release-fleet-agent.sh --native-candidate` entry exited 0 on the configured signing Mac. Source revision: `2e56535eb06c6ff25e137e77e57e53c8b41edbb9`, branch `codex/multi-user-server`; implementation revision: `fb164caa514b5a18cce2d2e8e0d37c21e5130101`.

## Publication

All three Apple submissions returned `Accepted`:

- Agent: `6d1d4ca3-ba15-4e4f-ad5c-afc177926e1d`
- Hub: `e3a0cc6b-18eb-47f4-a8e5-e178921070a4`
- DMG: `fc5e601d-6055-4524-8082-c99a770eb9f6`

Stapling, strict signature verification, Gatekeeper assessment and Sparkle signature verification passed. The candidate gateway verified all four SHA entries. Both packages were actually downloaded and compared before the current pointer switched to build 9; the published manifest and appcast then also matched the local files. The previous immutable release remains available for rollback.

```text
77791c61978e1f518099dba0e602e43b0a95bde978e8790cd95a0dd1610c641f  Fleet-Hub.dmg
e260901721ec6538d83f173ced06d1cf6ff6031cc8c7381b0c070ebb3d674a0d  Fleet-Hub-update.zip
```

Existing signing and update credentials were used; none were recreated or rotated.

## Installed applications and original shared components

The installed Hub's existing Sparkle updater downloaded build 9 and completed Install and Relaunch. Hub and the independently managed Agent both report `0.1.8+9`. Agent PID changed from `57782` to `57378`, runtime phase is `running`, binding remains complete and unlocked, and actual background disk access remains `verified`.

Canonical binding identity (excluding the synchronized display name) and settings SHA match the pre-update baseline. Every file in the independent runtime matches the embedded Agent payload; every file in the installed Hub matches the published signed bundle. Both installed applications are accepted as `Notarized Developer ID`. No `pending-update.json` remains.

```text
143c1d08f060d435186121e51e8300d50fdc6598cdce343781ed0f6355fabe96  Hub executable
afb2a87e587f386437c89e3b53990a73938b3dc9986d503479b3e9e9a3d485f3  Agent executable
```

All seven installed shared resources match the original source bytes: keeper, supervisor, resolver, Desktop environment helper, idle guard, shared service plist and Aqua environment plist. The original keeper PID `57816`, signed Node PID `57844` and shared listener PID `57852` remain running across the Agent update. The listener binds only `127.0.0.1:47682`, `/readyz` returns 200, and the Unix proxy is owned by the current UID with mode `0600`.

The real GUI environment contains `CODEX_APP_SERVER_WS_URL=ws://127.0.0.1:47682/rpc`; the legacy daemon variable is absent. The original Aqua one-shot helper exited 0. After the session request, the keeper proxy and shared listener have an established TCP connection.

GUI verification shows the new version/PID, running and ready device, synchronized computer name, normal disk access with the existing settings-page drag target, and the text link naming both Hub and Agent for uninstall.

## Verification

The official entry ran the complete project verification before building and publishing:

```text
ok  fleet-agent (cached)
ok  fleet-agent 0.605s [native IP/DERP TLS regression]
ok  fleet-enroll (cached)
ok  fleet-enroll/multiuser (cached)
JavaScript: 405 passed, 0 failed, 0 skipped
Swift: 82 tests, 0 failures
Shell/install/idle/uninstall/deployment regressions: passed
==> 全部验证通过 ✓
```

After installation, real HTTP requests from the gateway through its existing mesh proxy returned:

```text
/api/health                                  200 ok
/api/info                                    200 shared, connected after sessions=true
/api/sessions?assistant=codex&scope=all        200, 23 sessions
/api/chat/history [existing idle session]    200, 40 events
gateway healthz / readyz / device status     200
existing four gateway services               active
```

The new Agent initializes its Codex backend on the first session request: an initial info response reported connected=false, then sessions succeeded and info reported connected=true. The existing info endpoint reads current backend state; it does not initialize it. Device credentials were read privately and not printed. HTTPS checks validated the configured CA and target identity.

The current Desktop stdio server PID `36187` and its active chats were left running. A complete Desktop quit/reopen after this turn is still required for same-thread writer and App Tools UAT; App Tools currently report unsupported/not ready. Session/history success and correct Aqua environment do not claim that Desktop UAT has occurred. Browser UAT is also not claimed: the separate candidate origin uses a private CA that Chrome did not trust, and its security warning was not bypassed.

Local evidence: `/private/tmp/fleet-native-release.4lld18`, `/private/tmp/fleet-hub-build9-aqua-release-20261010.log`, `/private/tmp/fleet-build9-installed-verify-20261010.log`, and `/private/tmp/fleet-build9-gateway-verify-20261010.log`.
