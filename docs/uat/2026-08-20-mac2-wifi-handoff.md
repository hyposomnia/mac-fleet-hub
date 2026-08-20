# mac2 Wi-Fi/VNC diagnostics handoff

Date: 2026-08-20

## Scope

- Source Mac: `mac1` / `100.64.0.2`
- Problem Mac: `mac2` / `100.64.0.3`
- Office comparison Mac: `mac3` / `100.64.0.4`
- `.3` and `.4` are close together on the same office subnet (`10.18.64.0/19`) with gateway `10.18.95.254`.

## Confirmed findings

- `.2 -> .3` cannot establish a direct Tailscale connection and uses `DERP(fleet)`.
- Initial `.2 -> .3` RTT was `98-515 ms` with `157 ms` average and `63 ms` standard deviation.
- Effective SSH stream throughput was about `22 Mbit/s` from `.2` to `.3` and `5.8 Mbit/s` from `.3` to `.2`.
- Enabling UPnP and upstream DMZ for `.2` produced a stable public mapping, but did not establish a direct connection to `.3`.
- `.3` has no active macOS application firewall, VPN connection, or system proxy that explains the difference.
- `.3` and `.4` use Tailscale `1.98.5` and the same office gateway, but attach to different 5 GHz radios/channels.

| Metric | `.3` | `.4` |
|---|---:|---:|
| RSSI | `-63 dBm` | `-66 dBm` |
| Noise | `-96/-97 dBm` | `-96 dBm` |
| PHY | 802.11ax, 5 GHz, 20 MHz | 802.11ax, 5 GHz, 20 MHz |
| Channel | `161` | `165` |
| Negotiated rate | `206-229 Mbit/s` | stable `286 Mbit/s` |
| Gateway packet loss (50 packets) | `4%` | `0%` |
| Gateway RTT | `4.0/6.3/45.2 ms` | `4.4/5.8/10.8 ms` |
| Gateway RTT stddev | `6.6 ms` | `1.4 ms` |

At another sample, `.3` gateway RTT averaged `38.6 ms` and peaked at `102.6 ms`, while `.4` stayed near `5 ms`. The evidence points to the AP/radio on channel 161, Wi-Fi retransmissions, or AP uplink/load rather than weak RSSI.

The office NAT has no PCP, NAT-PMP, or UPnP. Its public UDP endpoint can change between office egress addresses, so direct Tailscale hole punching may remain unavailable even after the local Wi-Fi issue is corrected.

## Continue on `.3`

Run before and after reconnecting Wi-Fi:

```bash
ping -c 50 -i 0.1 10.18.95.254
tailscale netcheck
tailscale ping -c 10 100.64.0.2
```

Read the current Wi-Fi radio without `sudo`:

```bash
osascript -l JavaScript -e 'ObjC.import("CoreWLAN"); var i=$.CWWiFiClient.sharedWiFiClient.interface; JSON.stringify({rssi:Number(i.rssiValue),noise:Number(i.noiseMeasurement),txRate:Number(i.transmitRate),channel:Number(i.wlanChannel.channelNumber),band:Number(i.wlanChannel.channelBand),width:Number(i.wlanChannel.channelWidth),mode:Number(i.activePHYMode)})'
```

Next experiment: toggle Wi-Fi locally on `.3` near `.4`, then confirm whether it moves from channel 161 to channel 165 and whether gateway loss returns to 0%. macOS cannot directly pin a Wi-Fi channel; the durable fix is AP/BSSID steering or repairing the channel-161 AP/radio/uplink.

No VNC/SSH forwarding remains configured. The temporary `127.0.0.1:5901` tunnel on `.2` was removed and verified closed.
