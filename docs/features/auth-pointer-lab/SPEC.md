# 登录页鼠标跟随效果

## 目标与现状

用户要求调研当前登录页的鼠标跟随问题，提供五个可体验方案供选择。下文记录最初独立原型阶段；后续用户先选择 05 并取消表单遮罩，再要求替换为四周飞向鼠标的稀疏星光。最终实现见 [星光发布记录](../../releases/auth-starlight-2026-10-08/README.md)。

已读取用户指定测试服务的公开 HTML、`account.css` 与 `auth_effects.js`。服务返回的 effects 文件与仓库文件 SHA-256 相同：`3b632c9d822eeefa830e2b2a4e39de4ba929f1fba72a917557ec97f85c598253`。浏览器因 `ERR_CERT_AUTHORITY_INVALID` 无法直接打开该服务，未绕过浏览器证书警告；原型依据已核对的公开资源与本地源码制作。

当前方案是 32px 十字准星，旁边显示实时坐标；每帧直接更新鼠标位置并取整。它已有 fine pointer、减少动态效果和页面隐藏保护，但缺少缓动、局部材质反应及表单避让。实时变化的坐标与准星还容易把注意力从账号输入引走。这些属于设计判断，未声称现有实现有性能故障。

## 调研依据

2026-10-08 阅读的公开资料：

| 资料 | 观察 | 本次采用的结论 |
| --- | --- | --- |
| [Build UI · Spotlight](https://buildui.com/recipes/spotlight) | 径向渐变随鼠标移动，装饰层使用 pointer-events-none；动效位置更新与 React 渲染解耦 | 柔光可以提供反馈而不用追加坐标信息；本原型用原生 Canvas |
| [Codrops · Animated Custom Cursor](https://github.com/codrops/AnimatedCustomCursor) | 通过 SVG filter 制作多种光标形变，包含连续动画和插值 | 借鉴速度形变与跟随层次；保留系统光标，避免在登录场景中使用大幅扭曲 |
| [Motion · spring](https://motion.dev/docs/spring) | 刚度、阻尼和质量改变跟随手感；不受约束的弹簧可持续运行 | 控制跟随时长和收敛；这里用指数缓动及链式延迟，不引入物理引擎或 Motion 依赖 |
| [MDN · requestAnimationFrame](https://developer.mozilla.org/en-US/docs/Web/API/Window/requestAnimationFrame) | 回调频率随屏幕刷新率变化；应按 timestamp 计算动画进度 | 统一使用帧时间差，静止收敛后停止调度 |
| [MDN · prefers-reduced-motion](https://developer.mozilla.org/en-US/docs/Web/CSS/Reference/At-rules/@media/prefers-reduced-motion) | 系统偏好要求减少或移除非必要动效 | 停止跟随与自动演示，只保留静态背景 |

五个效果是基于这些机制制作的原创对比方案，未使用这些项目的框架或运行依赖。磁性点阵和金属掠光是针对 Fleet 配色与背景用途设计的探索。

## 五个方案

| 编号 | 渲染入口 | 行为 | 适用取向 |
| --- | --- | --- | --- |
| 01 | `softLight()` | 340px 柔光和细长次级光斑；跟随时间常数 130ms | 低干扰、安静 |
| 02 | `magneticDots()` | 32px 点间距，178px 作用半径；邻近点向外移动并增强亮度；85ms 跟随 | 工具感与反馈平衡，优先推荐 |
| 03 | `preciseRing()` | 18px 内环、27px 外侧弧；速度带来轻微椭圆形变；55ms 跟随 | 小幅反馈、轻量视觉 |
| 04 | `metallicLight()` | 29 条细曲线，随鼠标产生局部弯曲和银蓝亮度变化；220ms 跟随 | 更强的钛金属材质感 |
| 05 | `elasticRibbon()` | 26 个跟随点形成三股细丝，头部 45ms 跟随，链式延迟；静止后淡出 | 更明显的转向和拖尾表现 |

`1 - exp(-elapsed / duration)` 使收敛按时间计算。页签切换重置拖尾；系统偏好改变时取消正在调度的帧并回到静态状态。原型没有测量或承诺实际设备帧率。

## 共同交互

- 系统鼠标保持可见，效果层不会接收点击。
- 初次评审采用表单模糊遮罩及悬停/焦点淡出；按用户后续要求，当前原型及最终星光实现均已移除这些遮罩与抑制。
- 鼠标离开预览区域后淡出；收敛后停止 requestAnimationFrame 调度。页面隐藏时取消调度与自动演示，恢复时显示静态背景。
- 触屏不启用鼠标跟随。主动自动演示是评审功能，减少动态效果优先级更高。
- 五个方案复用同一表单和布局，效果开关、自动演示、方案标签与解释只出现在评审区。
- 明暗颜色沿用生产钛银配色。输入和按钮统一 12px 圆角，无常驻装饰描边；键盘焦点使用可见 outline。
- 860px 以下评审标签横向滚动；480px 以下评审工具纵向排列。控件触控目标至少 44px。

## 后续实现落点

从独立原型接入登录页的实现落点：

1. `server/dashboard/auth.html`：只调整装饰层，保持鉴权脚本、表单 ID 与账号流程不变。
2. `server/dashboard/auth_effects.js`：只保留选定渲染函数及必要生命周期逻辑，不带入五方案评审控件。
3. `server/dashboard/account.css`：维护背景层及主题适配，取消表单遮罩。
4. `server/dashboard/auth_effects.test.mjs`：继续验证 coarse pointer、减少动态效果、隐藏页面与销毁取消调度；对新实现补充有意义的行为覆盖。
5. 认证页 CSS/动效引用独立升级为 v188，绕开旧缓存键；运行 `bash scripts/verify.sh` 和独立本地浏览器验证，再按用户授权发布指定验收服务。

最初五方案评审未改动运行文件。后续星光修改仅涉及登录页 HTML、CSS、动效与测试；账号接口、LaunchAgent、Desktop、客户端二进制和网络配置未变。
