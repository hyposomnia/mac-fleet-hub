import { readFileSync, writeFileSync } from 'node:fs';
import { createHash } from 'node:crypto';
import { join } from 'node:path';
import { pathToFileURL } from 'node:url';

export function createNativeRelease(directory, metadata) {
  const origin = new URL(metadata.origin);
  if (origin.protocol !== 'https:' || origin.origin !== metadata.origin || origin.username || origin.password ||
      !Number.isSafeInteger(metadata.build) || metadata.build < 1 || !/^\d+\.\d+\.\d+$/.test(metadata.version) ||
      !/^[a-f0-9]{40}$/.test(metadata.revision)) throw new Error('Invalid immutable release identity or HTTPS origin');
  for (const receipt of [metadata.appNotary, metadata.dmgNotary]) {
    if (receipt?.status !== 'Accepted' || typeof receipt.id !== 'string' || !receipt.id) throw new Error('Both app and DMG must have Accepted notarization receipts');
  }
  const signature = Buffer.from(metadata.signature || '', 'base64');
  if (signature.length !== 64 || signature.toString('base64') !== metadata.signature) throw new Error('An Ed25519 update signature is required');
  const assets = Object.fromEntries([['dmg', 'Fleet-Hub.dmg'], ['update', 'Fleet-Hub-update.zip']].map(([kind, filename]) => {
    const bytes = readFileSync(join(directory, filename));
    if (kind === 'dmg' && (bytes.length < 512 || bytes.subarray(-512, -508).toString() !== 'koly') ||
        kind === 'update' && (bytes.length < 4 || bytes.readUInt32LE(0) !== 0x04034b50)) throw new Error('Release asset is not an application package');
    return [kind, { path: `/enroll/clients/${metadata.build}/${filename}`, sha256: createHash('sha256').update(bytes).digest('hex'), size: bytes.length,
      ...(kind === 'update' ? { ed_signature: metadata.signature } : {}) }];
  }));
  const manifest = { schema: 1, bundle_id: 'com.macfleet.fleet-hub', version: metadata.version, build: metadata.build,
    revision: metadata.revision, minimum_macos: '13.0', architectures: ['arm64', 'x86_64'], notarization: 'Accepted', assets };
  const escapeXML = value => String(value).replaceAll('&', '&amp;').replaceAll('"', '&quot;').replaceAll('<', '&lt;').replaceAll('>', '&gt;').replaceAll("'", '&apos;');
  const appcast = `<?xml version="1.0" encoding="utf-8"?>
<rss version="2.0" xmlns:sparkle="http://www.andymatuschak.org/xml-namespaces/sparkle"><channel>
<title>Fleet Hub</title><item><title>Fleet Hub ${metadata.version}</title>
<sparkle:version>${metadata.build}</sparkle:version><sparkle:shortVersionString>${metadata.version}</sparkle:shortVersionString>
<sparkle:minimumSystemVersion>13.0</sparkle:minimumSystemVersion>
<enclosure url="${escapeXML(metadata.origin + assets.update.path)}" length="${assets.update.size}" type="application/octet-stream" sparkle:edSignature="${metadata.signature}"/>
</item></channel></rss>\n`;
  return { manifest, appcast };
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  const [directory, metadataFile] = process.argv.slice(2);
  const { manifest, appcast } = createNativeRelease(directory, JSON.parse(readFileSync(metadataFile, 'utf8')));
  writeFileSync(join(directory, 'client-release.json'), JSON.stringify(manifest, null, 2) + '\n');
  writeFileSync(join(directory, 'appcast.xml'), appcast);
}
