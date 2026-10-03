# 已确认的图标角度

2026-10-04 用户确认生效第一次调整的图标，即最后对比图左侧“上一版”。采用 approved-logo.svg 的精确路径：提高俯视角、顶部略放大、两侧笔画略收薄；不采用第二版进一步向内收窄的轮廓。颜色、断口、画布大小及 App icon 的安全区保持原规范。

正式资源同步至 dashboard 的两处内联 Logo、favicon.svg、icon.svg、icon-maskable.svg 和 180/192/512px PNG。图标 URL 与 PWA 外壳升级 v171，以更新浏览器缓存。导出的各处 SVG 路径已与确认稿逐字核对，PNG 尺寸已检查。App icon 平台自身的更新时机由浏览器/系统决定。

## 生产验收

已发布不可变静态源码提交 `2bb3346`，PWA 外壳 `fleet-shell-v171`。发布前生产 105 个静态文件均与上一个源码基线 `20e3b13` 一致；发布后 105/105 SHA256 匹配新提交，运行时设备数据保留且非空。nginx、fleet-enroll、headscale、fleet-nodes.timer 均 active；公网未认证入口为 HTTP/2 302。

备份为 `/var/backups/mac-fleet-hub/dashboard-before-v171-20261003T180312Z.tgz`（文件名使用 UTC；本地发布日期为 2026-10-04）。没有重启服务、发布后台二进制、修改 Mac 或生产网络。Chrome 实际登录页强制刷新后检查网页左上 Logo 与浏览器标签图标，均显示确认版本。已安装 App 在操作系统层的图标刷新尚未单独验收。
