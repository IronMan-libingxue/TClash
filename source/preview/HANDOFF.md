# TClash Preview 工作交接

更新时间：2026-11-13（Asia/Shanghai）

## 目标和基线

- A 端仍是用户现有独立的 `~/Applications/Codex 用量.app`，负责真实统计、设备管理和密钥。不能把 A 合并进 TClash，也不能将本机 A 和同机 B 重复计为两台真实设备。
- TClash 以 Clash Verge Rev `v2.5.10`、提交 `c83f7ad529c7c9cb3d2d54a0955ef8c1b22a411b` 为基线，内置 B 端和侧栏 CodeX Speed。用量来自本机 Codex 日志；模型速度留在本机；同步遵循原 A/B 加密与策略。
- 设计和验收目标见同级的 `TClash-Preview-设计与验收方案.md`。其中的验收矩阵是目标，并非已经全部通过。
- 用户已报告原独立 Windows B 和 Mac A 实机配对验收成功；本轮尚未验证 Windows 版 TClash。不能把此前独立 B 的成功说成 TClash 已验收。

## 本轮实际进度

- 本机运行的是 `target/universal-apple-darwin/debug/bundle/macos/TClash Preview.app`；主窗口已停在 CodeX Speed，显示真实本机日志和模型速度估算。实测检测到当前用户同机安装了 A 端后，B 本地继续显示并暂停上传，避免重复统计；不读取或更改 A 的身份。此检测目前以同一操作系统用户的数据目录为范围。可从项目根目录运行：`open "$PWD/target/universal-apple-darwin/debug/bundle/macos/TClash Preview.app"`。
- 独立 A 端已原位更新，仍只有 `~/Applications/Codex 用量.app` 作为正式 A，数据目录和 Codex 配置未更改。更新前的应用保存在 `target/a-backup-path` 所指位置。先在隔离目录用现有配置副本验证设备记录和本机统计，再安装；安装后实际窗口仍能看到 Mac 1 真实数据和原有 Windows 1 记录。
- 更新后的 A 端可生成 TClash 新设备预授权包，也为现有 Windows 1 生成了迁移包：`~/Library/Application Support/Codex Usage Tracker/deployments-4112663194/Windows 1 TClash 迁移.zip`。迁移包在本机核对了内容和目标设备，未包含新授权文件或令牌，尚未在 Windows 上执行。
- 预览安装材料在 `target/tclash-dist-v3/`，包括 Mac 双架构包及 Windows x64、ARM64 两种安装包。Mac 包仅临时签名且未公证，Windows 安装包未签名；均为开发预览，不等于实际设备验收。
- 真实 GUI 中完成无节点标准 YAML 导入、`.tclash` 单配置导出和再导入；示例配置已从应用清理，样本文件保留在 `/tmp/tclash-preview-sample.yaml` 和 `/tmp/tclash-preview-export.tclash`。标准 Mihomo 接受样本 YAML，拒绝专用文件。另用预览环境自身的无节点配置构造标准 ZIP，实际导入并恢复，重启后用量页和同机保护正常；测试备份已移至 `target/acceptance-fixtures/`。还没有在原版 Clash Verge GUI 中逐项实测，也没有验证真实 WebDAV。
- Go 用量测试 `go test -race ./cmd/tclash-collector ./internal/usagetracker` 通过，覆盖模拟仓库中的 A/B 数据、加密、倍率、默认与调整后的同步周期、离线刷新、同机不上传、锁互斥及 TClash 新设备授权。Rust 针对专用格式、关联脚本、备份回滚、代理共存的测试通过，上游配置相关 22 项及代理控制 33 项测试通过；Web 构建通过。
- Windows x64 和 ARM64 完整预览安装包已在 Mac 上交叉构建，并从安装包解出程序核对了架构、服务和采集组件；均未在 Windows 实机运行。
- Windows 旧 B 身份交接已实现设备名校验、原身份复用、同一数据目录互斥、交接成功后备份旧启动项并禁用、失败时尝试恢复；模拟测试覆盖身份选择、错误设备拒绝、无新凭据包和锁互斥。安装脚本、故障恢复及真实历史和同步仍须 Windows 实测，不能提前宣称交接已成功。
- TClash 使用独立应用标识、配置目录和服务通道；预览启动时系统代理保持关闭。已增加仅在系统代理仍指向 TClash 时才清理的保护，但切换、接管、服务和 TUN 的完整真实场景还需实机验证。Windows 采用上游 Tauri 默认安装器，避免原版自定义脚本操作 Clash Verge 的服务和文件；隔离效果仍需 Windows 实机验证。
- macOS 首次正式安装时的订阅链接默认程序登记及恢复逻辑已写入；工作区预览不会执行登记。Windows 的登记和恢复也已编写，但两平台的安装、还原及权限交互尚未做实际验收。
- 现有 A 端保存的同步间隔为 1 分钟，本轮没有擅自覆盖；新设置的默认设计仍为 5 分钟，用户可以在 A 端调整。

## 必须完成的后续工作

1. 在 Windows 1 真机上验证现有独立 B 到 TClash B 的安全身份交接：保留设备身份和历史、失败可恢复、避免双采集或重复计数。先检查当前 B 与 TClash 状态，勿在验证前擅自停用旧 B。不得告知用户“现有 Windows B 已自动接续”。
2. 分别核对 x64、ARM64 的安装器、启动、托盘与服务。交叉构建成功不等于在对应架构实机通过；未公证或未签名预览包还会遇到平台的正常安装提示。
3. 在不触碰用户真实令牌的情况下，进一步验证新设备安装包的端到端授权、失败重试及回滚；正式 A 端继续保持独立和可回退。
4. 在 Mac 与 Windows 真机做真实 A/TClash B 配对、默认五分钟同步、修改策略、离线补传、休眠恢复、退出和托盘生命周期、重复计数检查。只通过模拟仓库测试不能称作真实跨网验收。
5. 验证原版 ZIP/WebDAV 备份往返、多类配置和资源路径、原版 GUI 不接受专用格式，以及系统代理/TUN/服务模式、Clash Verge 共存、链接默认程序恢复。当前“兼容所有上游配置”是目标，不是已证明的结论。
6. 整理正式签名与手动升级策略；首版暂不自动更新。Linux 支持列为后续适配，不应将当前上游 Linux 打包配置当成已安全适配的 TClash 发行版。

## 构建提示

- Go：`~/Library/Caches/codex-usage-tracker-build/go/bin/go`，缓存位于同一 `codex-usage-tracker-build` 目录。
- Rust：项目本地 `.toolchain/rustup`，使用 `RUSTUP_HOME="$PWD/.toolchain/rustup" rustup run 1.99.1 ...`。
- Windows 交叉构建使用项目内 `.toolchain/cargo-tools/bin/cargo-xwin` 和 `.toolchain/xwin-cache`，本机为此安装了 Homebrew LLVM、lld、NSIS 和 sevenzip。ARM64 构建对 `ring` 使用 `.toolchain/clang-wrapper`；完整流程仍以目标 Windows 实机验收为准。
- 前端：`npx --yes pnpm@12.16.1 web:build`。通用 Mac 包使用 `tauri build --debug --target universal-apple-darwin --bundles app`，需要预先准备两个架构及 universal 的 sidecar。
- 独立 A 端的源码改动在本项目 `codex-collector/`；原始 `codex-usage-tracker` 工作区未被覆盖。构建正式 A 的发布包时，将 `TRACKER_TCLASH_ASSETS` 指向上述四个 TClash 安装素材所在目录后运行 `cmd/usage-tracker/build.sh release`；缺少任何素材会失败。
- 测试和构建目录含本机数据路径，严禁把授权文件、访问令牌、设备密钥或真实用量提交到源码仓库或日志。
