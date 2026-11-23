<h1 align="center">TClash</h1>

<p align="center">跨平台桌面代理 · CodeX Speed · 多设备用量同步</p>
<p align="center"><a href="https://github.com/IronMan-libingxue/TClash/releases/tag/v2.5.12">下载 2.5.12</a> · <a href="docs/版本索引.md">查看版本纪要</a> · <a href="docs/设计与验收">设计与验收</a></p>

## 快速下载

| 平台 | 最新通用升级包 |
| --- | --- |
| Windows x64 | [双击安装的 EXE](https://github.com/IronMan-libingxue/TClash/releases/download/v2.5.12/TClash-2.5.12-Windows-x64-Setup.exe) |
| macOS Apple 芯片 | [Apple 芯片 PKG](https://github.com/IronMan-libingxue/TClash/releases/download/v2.5.12/TClash-2.5.12-macOS-AppleSilicon.pkg) |
| macOS Intel | [Intel PKG](https://github.com/IronMan-libingxue/TClash/releases/download/v2.5.12/TClash-2.5.12-macOS-Intel.pkg) |

已配对的设备使用同一系统账号安装。Mac 安装目标为「应用程序」中的 `TClash.app`。Windows 启动标准安装向导。新设备由「Codex 用量」A 端生成首次配对包。历史通用包从 [2.5.11 交付页](https://github.com/IronMan-libingxue/TClash/releases/tag/v2.5.11) 下载。

## 界面预览

**仪表盘用量卡片**　右侧汇总本机所有模型的参考金额和预算占比。示例的 25% 对应蓝色填充四分之一张卡片。

![2.5.12 本机安全预览的用量卡片，预算占比 25%](docs/images/dashboard-usage.png)

**CodeX Speed**　查看模型、项目、活动会话和参考金额。使用系统内置价表时显示「本地价格表」，接收并验证 A 端的价表后显示「价格表已更新」。

![本机安全预览的 CodeX Speed 页面，演示本地价格表提示](docs/images/codex-speed.png)

截图来自本机安全预览，使用演示用量。参考金额和预算占比按 A 端的参考价表与设定预算计算。

## A 端与 B 端

| 组件 | 用途 |
| --- | --- |
| Codex 用量（A 端） | 汇总本机及配对设备的原始用量，管理预算、设备显示倍率、价格表和状态通知 |
| TClash（B 端） | 保留代理界面和本机用量统计，向 A 端同步并跟随价表。已配对设备升级时沿用本地授权和历史 |

## 开发与版本记录

- [逐次改版和 Git 提交](docs/版本索引.md)
- [聊天确认与最终决议](docs/聊天确认与决议.md)
- [五份设计基线、技术路线及验收文档](docs/设计与验收)
- [技术栈与构建资料](docs/技术栈与构建.md)
- [历史制品索引](docs/历史制品索引.md) · [恢复说明](docs/历史归档恢复.md)
- [源码来源与历史快照边界](docs/来源与回溯.md)
- [归档验收与校验](docs/归档验收.md)

项目分别继承 FlClash、Clash Verge Rev 和 Magpie。许可证保存在各源码目录。
