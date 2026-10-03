#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
: "${FLEET_CMAKE:?提供 CMake 3.31 或以上的可执行路径}"
OUTPUT="${1:?提供独立构建输出目录}"
CACHE="${FLEET_RUNTIME_CACHE:-$HOME/Library/Caches/fleet-hub/runtime}"
case "$OUTPUT" in /private/tmp/*|/tmp/*) ;; *) echo '运行组件只构建到独立临时目录。' >&2; exit 2;; esac
mkdir -p "$OUTPUT/sources" "$OUTPUT/universal/bin" "$OUTPUT/licenses" "$CACHE"
while IFS=$'\t' read -r name directory url sha; do
  archive="$CACHE/$name.tar.gz"
  if [[ ! -f "$archive" ]]; then curl --proto '=https' --tlsv1.2 -fL --max-time 600 "$url" -o "$archive"; fi
  [[ "$(shasum -a 256 "$archive" | awk '{print $1}')" == "$sha" ]] || { echo "$name 源码校验失败" >&2; exit 1; }
  if [[ ! -d "$OUTPUT/sources/$directory" ]]; then tar -xzf "$archive" -C "$OUTPUT/sources"; fi
  for license in LICENSE LICENSE.txt LICENSE.md COPYING; do
    if [[ -f "$OUTPUT/sources/$directory/$license" ]]; then cp "$OUTPUT/sources/$directory/$license" "$OUTPUT/licenses/$name-$license"; fi
  done
done < <(node -e 'for(const [name,value] of Object.entries(require(process.argv[1]))) console.log([name,value.directory,value.url,value.sha256].join("\t"))' "$ROOT/scripts/settings-runtime-sources.json")
cp "$ROOT/scripts/settings-runtime-sources.json" "$OUTPUT/licenses/sources.json"
if grep -q '^set(LIBWEBSOCKETS_LIBRARIES websockets websockets_shared)$' "$OUTPUT/sources/libwebsockets-4.3.5/cmake/libwebsockets-config.cmake.in"; then
  patch -f -p1 -d "$OUTPUT/sources/libwebsockets-4.3.5" < "$ROOT/scripts/libwebsockets-static.patch"
fi
grep -q '^set(LIBWEBSOCKETS_LIBRARIES websockets)$' "$OUTPUT/sources/libwebsockets-4.3.5/cmake/libwebsockets-config.cmake.in"
for arch in arm64 x86_64; do
  prefix="$OUTPUT/$arch/prefix"
  mkdir -p "$prefix" "$OUTPUT/$arch"
  common=(-DCMAKE_BUILD_TYPE=Release -DCMAKE_OSX_DEPLOYMENT_TARGET=13.0 "-DCMAKE_OSX_ARCHITECTURES=$arch" "-DCMAKE_INSTALL_PREFIX=$prefix" "-DCMAKE_PREFIX_PATH=$prefix" '-DCMAKE_IGNORE_PREFIX_PATH=/opt/homebrew;/usr/local' -DBUILD_SHARED_LIBS=OFF -DBUILD_TESTING=OFF)
  build_cmake() {
    local name="$1" directory="$2"; shift 2
    "$FLEET_CMAKE" -S "$OUTPUT/sources/$directory" -B "$OUTPUT/$arch/$name" "${common[@]}" "$@"
    "$FLEET_CMAKE" --build "$OUTPUT/$arch/$name" --parallel 8
    "$FLEET_CMAKE" --install "$OUTPUT/$arch/$name"
  }
  build_cmake json-c json-c-json-c-0.18-20240915 -DDISABLE_EXTRA_LIBS=ON -DDISABLE_WERROR=ON
  build_cmake libuv libuv-v1.51.0 -DLIBUV_BUILD_TESTS=OFF
  uv_library="$prefix/lib/libuv.a"
  [[ -f "$uv_library" ]] || uv_library="$prefix/lib/libuv_a.a"
  build_cmake libwebsockets libwebsockets-4.3.5 -DLWS_HAVE_PIPE2=0 -DDISABLE_WERROR=ON -DLWS_WITH_SSL=OFF -DLWS_WITH_SHARED=OFF -DLWS_WITH_STATIC=ON -DLWS_WITHOUT_TESTAPPS=ON -DLWS_WITH_LIBUV=ON -DLWS_WITH_EVLIB_PLUGINS=OFF -DLWS_WITH_HTTP2=OFF -DLWS_WITH_PLUGINS=OFF "-DLWS_LIBUV_LIBRARIES=$uv_library" "-DLWS_LIBUV_INCLUDE_DIRS=$prefix/include"
  build_cmake ttyd ttyd-1.7.7 "-DLIBUV_LIBRARY=$uv_library"
  mkdir -p "$OUTPUT/$arch/libevent" "$OUTPUT/$arch/tmux"
  (
    cd "$OUTPUT/$arch/libevent"
    ac_cv_func_pipe2=no CFLAGS="-O2 -arch $arch -mmacosx-version-min=13.0" LDFLAGS="-arch $arch -mmacosx-version-min=13.0" "$OUTPUT/sources/libevent-2.1.12-stable/configure" --prefix="$prefix" --disable-shared --enable-static --disable-openssl --disable-samples --disable-libevent-regress
    make -j8 && make install
  )
  (
    cd "$OUTPUT/$arch/tmux"
    CFLAGS="-O2 -arch $arch -mmacosx-version-min=13.0" LDFLAGS="-arch $arch -mmacosx-version-min=13.0" LIBEVENT_CFLAGS="-I$prefix/include" LIBEVENT_LIBS="$prefix/lib/libevent.a" PKG_CONFIG=false "$OUTPUT/sources/tmux-3.6a/configure" --prefix="$prefix" --disable-utf8proc
    make -j8 && make install
  )
done
for binary in ttyd tmux; do
  lipo -create "$OUTPUT/arm64/prefix/bin/$binary" "$OUTPUT/x86_64/prefix/bin/$binary" -output "$OUTPUT/universal/bin/$binary"
  otool -L "$OUTPUT/universal/bin/$binary"
  if otool -L "$OUTPUT/universal/bin/$binary" | grep -E '/opt/homebrew|/usr/local|/private/tmp|/tmp/' | grep -v '^/'; then echo '运行组件仍依赖开发机库。' >&2; exit 1; fi
done
cp "$ROOT/mac/fleet-agent/fleet-attach.sh" "$OUTPUT/universal/bin/fleet-attach"
chmod 0755 "$OUTPUT/universal/bin/"*
printf '\n运行组件构建目录：%s\n' "$OUTPUT/universal"
