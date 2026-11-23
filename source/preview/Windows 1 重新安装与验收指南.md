# Windows 1：重新安装 TClash 和验收

这次采用新的 Windows 1 连接，从新安装授权首次启用时开始统计。Mac 1 的数据继续保留；Windows 1 的旧测试记录不接续。请先在 A 端完成旧设备清理，再生成新的 Windows 1 TClash 安装包。

## 在 Mac 1 上生成和传送

1. 在 GitHub 为 Windows 1 生成新的设备令牌：只选择用量同步私库，仓库的 Contents 权限为 Read and write。不要在聊天中发送令牌。
2. 在独立的「Codex 用量」A 端进入「同步设置」，设备选择 **Windows 1**，将新令牌直接粘贴到「设备访问令牌」，点击「生成 TClash 安装包」。不要选择旧版 B 安装包或旧的迁移包。
3. 使用 A 端显示的安装包目录，找到新生成的 `Windows 1 TClash.zip`，通过可信的私下方式传到 Windows 1。该文件包含 Windows 1 的设备令牌，不要公开分享，也不要传到聊天。

## 在 Windows 1 上安装

1. 从系统托盘完全退出正在运行的 TClash Preview，以及旧的独立 Codex Usage Tracker；如果旧独立 B 设置了开机自启，也将其关闭。
2. 按 `Win + R`，输入 `%APPDATA%` 并打开。若以前运行过 TClash B，仅删除 `TClash Preview\collector` 文件夹；不要删除 TClash 的其他配置。若 `Codex Usage Tracker` 文件夹确定是此前测试用的独立 B，也删除该旧 B 数据文件夹。若不能确认其角色，先暂停并核对，不要删除。
3. 不要删除用户目录里的 `.codex` 文件夹，不要删除现有 Codex 日志，也不要清除 TClash 的订阅或代理规则。
4. 将新的 `Windows 1 TClash.zip` 解压到本机，双击 `TClash Install.cmd`。它会识别 Windows 的处理器类型，安装或更新对应版本，然后应用这台设备的新授权。安装包内也有 `README.tclash.windows.txt`，可随时查看。
5. 如果 Windows 提示安装包来源或安装失败，请先查看提示并反馈具体内容；本次是本地构建的预览版，Windows 安装包尚未完成正式签名。

## 验收

1. 打开 TClash 的 **CodeX Speed**。新连接首次在本机启用前的 Codex 请求不会计入；新装后暂时没有用量是正常情况。
2. 在 Windows 1 的 Codex 运行一条新任务。等待同步后，在 Mac 1 的 A 端查看 Windows 1 卡片和明细；首次同步可能需要稍等。
3. 核对 A 上只有一个 Windows 1，显示的 Token 为该设备真实用量；B 显示按 A 为该设备设定的系数换算，默认 1.5 倍。A 的账号官方额度仍属于共享账号整体。
4. 在 A 端修改 Windows 1 显示名称，确认名称变化而设备与数据没有重复；随后重启 TClash，确认新任务继续同步。网络暂时断开时，恢复联网后再检查补传。
5. A 端的上传频率可以调整；本次升级保留用户此前设置，当前为 1 分钟。默认设计为 5 分钟，不保证断网或设备休眠时实时更新。

以上是实机操作和验收步骤；只有完成 Windows 1 的实际安装和核对，才能确认跨设备同步验收通过。
