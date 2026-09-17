#!/usr/bin/env bash
# 回归守卫：shell 里 `$VAR` 后面紧跟非 ASCII 字符（如「」（）等）时，macOS 自带的
# /bin/bash 3.2 会把多字节字符的首字节并进变量名，于是 `$NOTARY_PROFILE` 紧跟一个
# 「」就被解析成不存在的变量名；在 `set -u` 下直接 `unbound variable` 退出，在没开
# set -u 时则静默展开成空串。两种都很难查，统一要求写成 `${VAR}`。
# （本测试不豁免注释行：注释里也照写 `${VAR}`，示例才不会被自己的守卫命中。）
#
# 背景：2026-09-16 发布 fleet-agent 时，scripts/release-fleet-agent.sh 的公证凭据预检
# 就因为这个写法在签名机上直接失败（`NOTARY_PROFILE?: unbound variable`），把整条
# 官方发布入口挡在第一步。本机没有 homebrew bash 5，只能靠写法规避。
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."

fail=0
while IFS= read -r file; do
  # LC_ALL=C 让 grep 按字节匹配；[^ -~] 即匹配任意非可打印 ASCII 字节（多字节字符的每一字节都算）。
  while IFS= read -r hit; do
    printf '✗ %s:%s\n' "$file" "$hit"
    fail=1
  done < <(LC_ALL=C grep -nE '\$[A-Za-z_][A-Za-z0-9_]*[^ -~]' "$file" || true)
done < <(find . -name '*.sh' -type f -not -path './.git/*' | sort)

if [[ "$fail" != 0 ]]; then
  echo "以上位置请改成 \${VAR} 形式（见本测试头部说明）。" >&2
  exit 1
fi

echo "bash-var-brace tests passed"
