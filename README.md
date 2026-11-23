<div align="center">

<img src="source/tclash/assets/images/icon.png" width="92" alt="TClash 应用图标">

# TClash

**在同一个桌面应用里管理代理，查看 Codex 的速度、用量和预算进度。**

支持 macOS 与 Windows。配对后，可在「Codex 用量」汇总多台设备的使用情况。

[安装](#安装) · [功能](#功能) · [用量怎么算](#用量怎么算) · [数据与隐私](#数据与隐私) · [版本记录](#版本记录)

<img src="docs/images/dashboard-usage.png" width="820" alt="TClash 仪表盘的 Codex 用量卡片，示例显示 25% 的预算占比">

<sub>仪表盘安全预览，使用演示数据。</sub>

</div>

## 安装

当前发行版为 **TClash 2.5.12**。选择对应的安装包，双击后按向导完成。

| 平台 | 安装包 | 安装方式 |
| --- | --- | --- |
| macOS · Apple 芯片 | [下载 PKG](https://github.com/IronMan-libingxue/TClash/releases/download/v2.5.12/TClash-2.5.12-macOS-AppleSilicon.pkg) | 在系统安装器中安装到「应用程序」 |
| macOS · Intel | [下载 PKG](https://github.com/IronMan-libingxue/TClash/releases/download/v2.5.12/TClash-2.5.12-macOS-Intel.pkg) | 在系统安装器中安装到「应用程序」 |
| Windows · x64 | [下载 EXE](https://github.com/IronMan-libingxue/TClash/releases/download/v2.5.12/TClash-2.5.12-Windows-x64-Setup.exe) | 双击启动 Windows 安装向导 |

**已配对设备升级**　使用同一系统账号安装，沿用原设备身份、授权、本机历史和代理配置。

**新设备接入**　在「Codex 用量」（A 端）生成该设备的首次配对安装包。本机使用统计在 TClash 的 **CodeX Speed** 页面查看。

[2.5.12 发布说明与校验值](https://github.com/IronMan-libingxue/TClash/releases/tag/v2.5.12) · [历史版本](https://github.com/IronMan-libingxue/TClash/releases)

## 功能

- **代理与连接**　管理代理配置和策略组，切换出站模式，查看实时连接和流量。
- **本机用量卡片**　仪表盘显示当前 Codex 模型、速度和参考金额。卡片右侧汇总本机所有模型的参考金额与预算占比，蓝色填充表示已用比例。
- **CodeX Speed 明细**　按统计周期查看本机的模型和项目用量，可切换是否计入缓存 Token。页面显示活动会话及已识别的 Fast、Ultrafast 状态。
- **多设备总览**　「Codex 用量」（A 端）汇总本机和配对设备的用量，可查看设备、项目和请求明细，并统一设置预算、参考价表与设备显示倍率。

<div align="center">
<img src="docs/images/codex-speed.png" width="820" alt="CodeX Speed 安全预览页面，显示本地价格表、模型用量和缓存选项">

<sub>CodeX Speed 安全预览，使用演示数据。</sub>

</div>

## 用量怎么算

| 指标 | 口径 |
| --- | --- |
| 输出速度 | 以最近可用的 Codex 调用估算每秒输出的 Token 数。首 Token 耗时有效时，从总耗时中扣除这段等待 |
| Token 用量 | 从本机 Codex 会话记录读取输入、输出和缓存用量，按所选周期汇总 |
| 参考金额 | 按所用模型的输入、输出和缓存单价估算，并应用可确认的服务档位规则。缺少价格时显示「未计价」 |
| 预算占比 | 将参考金额除以设定的参考预算。A 端配置的设备倍率用于展示，原始用量保留供对照 |

A 端可选择 Magpie 使用的 Models.dev 价表或 Agent Speed 价表，并设置每日更新。TClash 使用内置价表时显示「本地价格表」，接收并验证 A 端价表后显示「价格表已更新」。[计价与预算说明](docs/设计与验收/04-A端用量计价与通知.md)

## 数据与隐私

用量采集器读取本机 Codex 已生成的会话记录，从中提取 Token 数、模型、项目分组和会话状态等统计字段。配对后，TClash 将用量快照加密发送至配对所用的私有仓库，由 A 端汇总。[同步与价表验证说明](docs/设计与验收/05-B端FlClash迁移.md)

## 从源码构建

| 组件 | 源码与资料 |
| --- | --- |
| TClash 桌面端 | Flutter / Dart，基于 FlClash 和 Mihomo。[桌面源码](source/tclash) · [2.5.12 源码标签](https://github.com/IronMan-libingxue/TClash/tree/source-v2.5.12) |
| Codex 用量 | Go 用量采集器及 A 端应用。[用量采集源码](source/preview/codex-collector) |
| 打包 | macOS 使用 Xcode 和系统安装器，Windows x64 使用 Inno Setup。[技术栈、平台配置与原打包输入](docs/技术栈与构建.md) |

## 版本记录

- 逐次改版、对应提交以及已确认的产品决策。[版本索引](docs/版本索引.md) · [聊天确认与决议](docs/聊天确认与决议.md)
- 原始设计基线、验收标准和各阶段的源码范围。[设计与验收](docs/设计与验收) · [源码来源](docs/来源与回溯.md)
- 历史制品、完整性校验和离线恢复步骤。[历史制品索引](docs/历史制品索引.md) · [归档恢复](docs/历史归档恢复.md) · [归档验收](docs/归档验收.md)

## 上游与许可

桌面端基于 FlClash，早期 TClash Preview 基于 Clash Verge Rev，用量组件源自 Magpie。[桌面端许可证](source/tclash/LICENSE) · [早期 Preview 许可证](source/preview/LICENSE) · [用量组件许可证](source/preview/codex-collector/LICENSE)

本页的结构参考 Agent Speed。该项目也提供 A 端可选的参考价格来源。[Agent Speed](https://github.com/IronMan-libingxue/agent-speed)
