#!/usr/bin/env bash

run_candidate_release() {
  : "${FLEET_RELEASE_BUILDER_IP:?}" "${FLEET_CANDIDATE_BRANCH:?}" "${FLEET_CANDIDATE_WEB_BASE:?}"
  : "${FLEET_CANDIDATE_GATEWAY_SSH:?}" "${FLEET_CANDIDATE_ROOT:?}"
  local target="$FLEET_CANDIDATE_GATEWAY_SSH" port="${FLEET_CANDIDATE_GATEWAY_PORT:-22}"
  local candidate_root="$FLEET_CANDIDATE_ROOT" branch="$FLEET_CANDIDATE_BRANCH"
  local tailscale_bin="${FLEET_RELEASE_TAILSCALE_BIN:-$(command -v tailscale || true)}"
  [[ -x "$tailscale_bin" ]] || die '缺少 tailscale。'
  [[ "$("$tailscale_bin" ip -4 | head -n1)" == "$FLEET_RELEASE_BUILDER_IP" ]] || die '当前机器不是唯一签名构建机。'
  [[ "$branch" == codex/* && "$branch" != *[\ \'\"\;]* ]] || die '候选发布必须显式指定 codex/ 特性分支。'
  [[ "$candidate_root" =~ ^/opt/[a-zA-Z0-9_-]+$ ]] || die '候选根目录必须是 /opt 下专属实例。'
  [[ "$port" =~ ^[0-9]+$ ]] || die 'SSH 端口无效。'
  node -e 'const url = new URL(process.argv[1]); if (url.protocol !== "https:" || url.origin !== process.argv[1] || url.username || url.password) process.exit(1);' "$FLEET_CANDIDATE_WEB_BASE"
  local curl_args=(--proto '=https' --tlsv1.2 -fsS --max-time 300)
  if [[ -n "${FLEET_CANDIDATE_CA_FILE:-}" ]]; then curl_args+=(--cacert "$FLEET_CANDIDATE_CA_FILE"); fi
  cd "$ROOT"
  [[ "$(git branch --show-current)" == "$branch" ]] || die '候选分支不匹配。'
  [[ -z "$(git status --porcelain)" ]] || die '候选源码必须是干净的不可变提交。'
  security find-identity -v -p codesigning | grep 'Developer ID Application:' || die '缺少 Developer ID。'
  require_notary_credentials
  ssh_retry "$port" "$target" '验证专属候选实例标记' "test -f '$candidate_root/.candidate-instance' && test -d '$candidate_root/current/dashboard'"
  curl "${curl_args[@]}" "$FLEET_CANDIDATE_WEB_BASE/healthz"
  if [[ "${FLEET_NATIVE_CANDIDATE:-0}" == 1 ]]; then
    source "$ROOT/scripts/lib/native-candidate-release.sh"
    native_candidate_preflight
  fi
  if [[ "$MODE" == candidate-check ]]; then echo '候选发布预检通过；未修改任何服务。'; return; fi
  step "拉取候选分支 $branch 并验证"
  GIT_SSH_COMMAND='ssh -o BatchMode=yes' git pull --ff-only origin "$branch"
  [[ "$(git rev-parse HEAD)" == "$(git rev-parse "origin/$branch")" ]] || die '候选源码不等于远端分支。'
  bash "$ROOT/scripts/verify.sh"
  if [[ "${FLEET_NATIVE_CANDIDATE:-0}" == 1 ]]; then run_native_candidate_release; return; fi
  local work
  work="$(mktemp -d /private/tmp/macfleet-candidate-release.XXXXXX)"
  chmod 0700 "$work"
  step '构建正式双架构客户端并等待 Apple 公证 Accepted'
  FLEET_CODESIGN_IDENTIFIER=com.macfleet.fleet-agent FLEET_NOTARY_PROFILE="$NOTARY_PROFILE" FLEET_NOTARY_RESULT_FILE="$work/notary.json" bash "$ROOT/mac/fleet-agent/build.sh"
  for asset in "$AMD_ASSET" "$ARM_ASSET"; do codesign --verify --deep --strict "$asset"; done
  "$ARM_ASSET" capabilities > "$work/capabilities.json"
  node -e 'const value = JSON.parse(require("fs").readFileSync(process.argv[1])); if (value.device_authorization !== 1 || value.browser_pairing !== 1 || value.agent_proxy !== 1) process.exit(1);' "$work/capabilities.json"
  git add "$AMD_ASSET" "$ARM_ASSET"
  if ! git diff --cached --quiet; then git commit -m 'build(agent): publish isolated multi-user candidate'; fi
  git push origin "$branch"
  local revision
  revision="$(git rev-parse HEAD)"
  mkdir -p "$work/package" "$work/distribution/dist"
  git archive "$revision" mac | tar -xf - -C "$work/package"
  COPYFILE_DISABLE=1 tar -czf "$work/distribution/mac-bundle.tar.gz" -C "$work/package" mac
  cp "$AMD_ASSET" "$ARM_ASSET" "$work/distribution/dist/"
  git show "$revision:server/enroll/bootstrap.sh" > "$work/distribution/bootstrap.sh"
  node "$ROOT/scripts/client-release-manifest.mjs" "$work/distribution" "$revision" "$work/notary.json"
  (cd "$work/distribution" && shasum -a 256 bootstrap.sh mac-bundle.tar.gz release.json dist/fleet-agent-darwin-amd64 dist/fleet-agent-darwin-arm64) > "$work/SHA256SUMS"
  local remote_input="/tmp/macfleet-candidate-$revision"
  ssh_retry "$port" "$target" '创建候选暂存目录' "umask 077; mkdir -p '$remote_input'"
  scp -o BatchMode=yes -o ConnectTimeout=8 -P "$port" -r "$work/distribution" "$work/SHA256SUMS" "$target:$remote_input/"
  step '只发布验收安装源，不替换当前服务或客户端'
  ssh_retry "$port" "$target" '原子发布独立候选下载源' "sudo -n bash -c 'set -e; test -f \"$candidate_root/.candidate-instance\"; cd \"$remote_input/distribution\"; sha256sum -c ../SHA256SUMS; install -d -m 0755 \"$candidate_root/client-releases\"; test ! -e \"$candidate_root/client-releases/$revision\"; cp -R . \"$candidate_root/client-releases/$revision\"; find \"$candidate_root/client-releases/$revision\" -type d -exec chmod 0755 {} +; find \"$candidate_root/client-releases/$revision\" -type f -exec chmod 0644 {} +; ln -s \"$candidate_root/client-releases/$revision\" \"$candidate_root/client-next-$revision\"; mv -Tf \"$candidate_root/client-next-$revision\" \"$candidate_root/client-current\"'"
  for asset in bootstrap.sh mac-bundle.tar.gz release.json dist/fleet-agent-darwin-amd64 dist/fleet-agent-darwin-arm64; do
    mkdir -p "$work/downloaded/$(dirname "$asset")"
    curl "${curl_args[@]}" "$FLEET_CANDIDATE_WEB_BASE/enroll/$asset" -o "$work/downloaded/$asset"
    cmp "$work/distribution/$asset" "$work/downloaded/$asset"
  done
  codesign --verify --deep --strict "$work/downloaded/dist/fleet-agent-darwin-arm64"
  codesign --verify --deep --strict "$work/downloaded/dist/fleet-agent-darwin-amd64"
  echo "候选安装包真实下载及 SHA/签名核验通过：revision=${revision}，日志与公证记录位于 ${work}。未更新旧服务或任何 Mac。"
}
