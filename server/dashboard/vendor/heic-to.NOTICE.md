# HEIC preview decoder

`heic-to.js` is the unmodified IIFE distribution of **heic-to 1.6.5** by
Hopper Gee, using libheif 1.23.5 and libde265 1.0.16. It is licensed under
LGPL-3.0-or-later. The LGPL and accompanying GPL texts are in
`heic-to.LICENSE` and `heic-to.GPL`.

The browser loads this independent library only when native HEIC/HEIF display
fails. Conversion runs in a local Web Worker. No image is sent to another
service. Original file downloads are unchanged. To use a modified compatible
decoder, replace this script while keeping the global `HeicTo` function API.

Upstream distribution and corresponding sources:

- Original npm package: https://registry.npmjs.org/heic-to/-/heic-to-1.6.5.tgz
- heic-to source and build instructions:
  https://github.com/hoppergee/heic-to/tree/f6b3c42d02e6118e1b88fb279baf0fc6184747f8
- libheif source and Emscripten build script:
  https://github.com/strukturag/libheif/tree/v1.23.5
- libde265 source: https://github.com/strukturag/libde265/tree/v1.0.16

The npm tarball SHA-512 (base64) is
`xPH6gvrzSRIFo/GyzYbr+DZX1JoQliw1oJBT1PCduR0ruDVs1DjlAdU1m5Thed2ZXBoLqYMCd5UXiJteRl3BNQ==`.
The script comes from `package/dist/iife/heic-to.js` in that archive.
