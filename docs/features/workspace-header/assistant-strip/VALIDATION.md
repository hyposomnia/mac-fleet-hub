# 铺满助手切换栏

ChatGPT / DeepSeek Tab 铺满桌面会话列表的第二行，移除外层软底圆角及外侧上下/左右留白；保留选中项的白底圆角、原有鲸鱼入口和切换事件。移动布局沿用原规则。

Chrome 静态隔离预览实际测量：会话列宽 330px，Tab 外壳宽 330px；左右外侧及顶部间距均 0px，行高 42px，背景 rgb(227, 233, 239)。本地选择 DeepSeek 后 aria-selected=true，选择 ChatGPT 后恢复选中态。示例夹具显式显示 DeepSeek，未连接真实设备或请求真实助手切换。

`bash scripts/verify.sh` 退出码 0，网页测试 303 项通过、0 失败，Go/Swift/shell 验证通过。原始输出：[verify.txt](verify.txt)。

本次仅源码与独立本地验证，没有生产部署。

![最终效果](preview.png)
