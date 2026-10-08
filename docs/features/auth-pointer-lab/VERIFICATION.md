# 验证记录

日期：2026-10-08。以下为最初五方案原型的历史验证记录。用户后续取消表单遮罩并改选星光；当前星光验证见 [发布记录](../../releases/auth-starlight-2026-10-08/README.md)，不能把本目录首次截图当作当前效果证据。

## 命令输出

`node --check docs/features/auth-pointer-lab/lab.js`：退出码 0，无语法错误输出。

`curl -I http://127.0.0.1:8788/`：

```text
HTTP/1.0 200 OK
Server: SimpleHTTP/0.6 Python/3.13.13
Content-type: text/html
```

`lsof -nP -iTCP:8788 -sTCP:LISTEN`：

```text
COMMAND   PID USER   ... NAME
Python  47533  hjc   ... TCP 127.0.0.1:8788 (LISTEN)
```

服务只读分发本目录。为供用户交互评审保留当前服务，PID 同时记录在 `/private/tmp/macfleet-auth-pointer-lab.pid`。评审完成后仅停止该 PID；不要使用宽泛的进程匹配清理。

## 浏览器验证

- 五个方案 × 浅色/深色 × 桌面/移动端，共 20 个状态。
- 桌面视口 1440×900；每个状态的页面宽度为 1440，没有横向溢出。
- 移动视口 390×844，启用触屏模拟；每个状态的页面滚动宽度不超过 390，评审标签在自身容器内横向滚动。
- 每次切换后选中标签、页面 query 和主题均对应目标方案。全部状态的截图位于 `screenshots/`，布局读数位于 `browser-checks.json`。
- 页面控制台 error / warn 读取结果：`[]`。
- 通过真实鼠标输入，Canvas 像素输出随位置改变；关闭效果后 Canvas alpha 总和为 0。
- 减少动态效果模拟下显示静态提示，自动演示按钮禁用；鼠标输入前后 Canvas 像素读数不变。
- 全画布像素读数：减少动态效果前/后均为 `alpha=56095, weighted=28725140`；触屏模式前/后均为 `alpha=47289, weighted=24214015`。
- 左右箭头可切换方案并把焦点移到目标标签。
- “继续”按钮显示预览反馈；观察区间的 Network.requestWillBeSent 事件为 `[]`，没有发出鉴权或生产请求。
- 原型字段只读，用户无法向原型提交真实账号密码。

移动端结果来自浏览器模拟，未作为手机真机、软键盘或 PWA 证据。此次为独立原型交付，没有提交、部署或修改生产代码；项目提交/部署前验证入口尚未因本次原型运行。
