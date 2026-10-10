# Native shared release verification — 2026-10-09

## Installed and published build 8

Source: `2082006b2f4b50380fadc9f49a86dce0d0690fa0`, branch `codex/multi-user-server`.
Version: `0.1.7+8`. Official native release entry completed with exit 0.

Agent, Hub and DMG notarization all returned `Accepted`:

- Agent: `ff541bb8-106e-42ba-9a4d-ed5095d6f767`
- Hub: `88de6a64-a6e9-40d5-ada7-6662544e3ecd`
- DMG: `0bc9b7d8-8900-4c2f-91a7-a181ebbe05a7`

Staple, Gatekeeper, Sparkle signature and actual downloaded artifact comparisons passed before publication pointer switch.

```
58bbd0831c0e64eb11c176a4a6b5a87530dc39626c6f84791bbb0ca58f7c6b89  Fleet-Hub.dmg
3a559e58f28a7095739a1b5e02579c8e2b9ff6c9ec997b32afc0ffd8602ce7e2  Fleet-Hub-update.zip
```

The existing Sparkle updater installed Hub and independent Agent. Control status returned `0.1.7+8`, PID `57782`, runtime `running`, complete binding, unlocked authorization, and background disk access `verified`. Both installed bundles were accepted as `Notarized Developer ID`. Settings and binding identity digests remained unchanged; the expected device name synchronized from the server. No update journal remained.

The existing abj acceptance backend was backed up and replaced from the same source solely to return the device-name fields already implemented by the client. Database retained one user and two devices. 139 deployment SHA checks, health/ready responses, and four existing services passed.

Actual gateway-to-device HTTP verification used the existing private device credentials without printing them:

```
/api/health                                  200  ok
/api/info                                    200  shared, connected=true
/api/sessions?assistant=codex&scope=all        200  23 sessions
```

Loopback `/readyz` passed, the original keeper proxy was owned by the current UID with mode `0600`, and the shared launchd service ran. The live Desktop still used its existing stdio server; it was not interrupted.

## Build 9 source correction and verification

Source: `fb164caa514b5a18cce2d2e8e0d37c21e5130101`.

Actual installation verification found the Aqua environment missing despite the user-domain value. Native packaging now also carries the original Aqua plist and runs the existing environment helper through it, verifies GUI values, and restores user/Aqua snapshots independently. Existing snapshot format remains readable.

The original helper's BSD sed parser returned an empty port on this Mac. The corrected ERE parser is tested with system sed for IPv4, localhost, IPv6, missing hosts and invalid ports; the same helper remains the only readiness/injection implementation.

Red evidence: two new native tests failed for the missing Aqua definition and domain snapshot; the packaging test failed for its missing original plist. The real-parser shell test failed on the normal loopback URL before correction.

Actual native lifecycle UAT with real launchd and the existing ready listener, without stopping the real keeper or touching Desktop:

```
original_aqua_helper_applied=true
aqua_and_user_environment_restored=true
```

Full project verification after correction:

```
Go agent, original IP/DERP TLS verification and server tests: passed
JavaScript: 405 passed, 0 failed, 0 skipped
Swift: 82 tests, 0 failures
Shell and deployment regressions: passed
==> 全部验证通过 ✓
```

Build 9 Universal development bundle compiled successfully. Both Hub and Agent contain arm64/x86_64. All seven original shared resources match source bytes. This isolated preview is unsigned and has not been installed.

Build 9 formal release and local replacement completed on 2026-10-10 after the user unlocked the Mac. The existing notary profile became readable without recreating credentials. Agent, Hub and DMG all returned Accepted; the official entry published the verified downloads, and the built-in updater installed `0.1.8+9` for Hub and independent Agent. Binding/settings and disk access remained intact, the actual Aqua environment is now correct, and gateway session/history HTTP checks passed. See [build 9 release evidence](native-build9-aqua-release-2026-10-10.md).

Shared same-thread Desktop writer/App Tools UAT still requires a safe, explicit Desktop reopen after the active turn completes. API sessions success does not claim that final Desktop UAT has happened.
