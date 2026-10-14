# 会话文件预览访问范围

会话生成的日志、媒体和附件可能位于临时目录、外接磁盘或项目目录。已授权设备的预览和下载按 Fleet Agent 进程的操作系统权限读取文件，不再把文件浏览器根目录作为只读预览的边界。服务端账号、设备归属及客户端授权检查保持不变。

| 需求 | 可观察行为 | 测试 | 实现 |
| --- | --- | --- | --- |
| REQ-1 | 浏览器根目录之外的绝对路径、可读符号链接及带行列后缀路径可预览和下载 | `TestFilePreviewReadsOSAccessiblePathsOutsideBrowserRoot` | `resolvePreviewPath`、预览及内容 handler |
| REQ-2 | 系统拒绝读取返回 403 `file_permission_denied`，缺失文件返回 404 | `TestFilePreviewDistinguishesMissingAndOSDeniedFiles` | `writePreviewPathError`、文件读取错误分支 |
| REQ-3 | 相对路径按 cwd 解析，无 cwd 时沿用文件浏览器起始目录；原有格式、文本大小、普通文件约束保留 | `TestFilePreviewReturnsMarkdownAndResolvesRelativePaths`、现有预览测试 | `resolvePreviewPath`、预览 handler |

此变化扩大只读预览和下载到进程可读文件，不扩大上传、删除、重命名或目录浏览的权限。macOS 完全磁盘访问与 Unix 权限仍由系统执行。`FLEET_FILE_ROOT` 保持文件浏览器边界，并作为未提供 cwd 的相对预览路径基准。

交付仅包含 SaaS 分支源码与验证。正式生效须后续获准后由签名机构建、公证和发布 Mac Agent；本次不替换运行中的客户端或安装包。回滚采用上一正式签名版本。
