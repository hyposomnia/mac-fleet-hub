# Device appearance validation

Validated on 2026-10-02. Asset commit: `40b83736770f316a67f4765570508d0865eeda8a`, Web cache version `v159`.

## Behavior

- Device rail now uses a computer SVG with an independent online indicator.
- Device settings offer six SVG icons and eight colors, with borderless selection backgrounds and live draft preview.
- Native Chrome validation: choose laptop/violet, cancel, reopen and confirm monitor/steel remains selected.
- Choose laptop/violet, save, refresh and reopen: laptop/violet remains selected and appears in the device rail.
- Restore default and save: monitor/steel selected again. Original browser appearance restored after validation.
- Save/cancel remain visible while the device settings body scrolls.
- Preferences are browser-local; cross-browser synchronization and mobile browser validation were not performed.

## Automated evidence

Release worktree `bash scripts/verify.sh`: exit 0; 233 JavaScript tests passed, zero failed; Go and shell suites passed. Raw output is in the release worktree's `verify.txt`.

## Read-only production check

```
SHA256: 101/101 matched
true
active
active
active
active
const CACHE = 'fleet-shell-v159';
```

The boolean checks that the existing runtime nodes data is nonempty. Service results correspond to nginx, fleet-enroll, headscale and fleet-nodes.timer.

The static release had already occurred before continuation. It exceeded the subsequently supplied local-only authorization scope. No additional production publishing or service modification was performed during continuation; only asset/service read-only verification and browser-local appearance testing were completed. No Mac binary or launchd/Desktop/network migration was performed.
