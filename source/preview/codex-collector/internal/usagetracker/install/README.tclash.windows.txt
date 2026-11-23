TClash 远端设备重新接入（Windows）

本安装包含该设备的私有访问令牌，请只传送到指定设备，不要公开或发送到聊天。
新设备从接入时开始统计。原有 Codex 日志和 TClash 代理配置不需要删除。

1. 在 GitHub 上撤销该设备的旧访问令牌；不要撤销 A 端使用的令牌。
2. 从托盘退出 TClash Preview 和旧的 Codex Usage Tracker。若旧采集端设置了开机自启，请关闭其自启。
3. 如果此前测试过 B，打开“运行”，输入 %APPDATA%。
   仅删除“TClash Preview”里的“collector”文件夹。不要删除 TClash 的代理配置目录。
   旧独立 B 的数据位于“Codex Usage Tracker”文件夹；确认它是本机旧 B 后删除该文件夹。
   不要删除用户目录下的 .codex 文件夹或其他 Codex 数据。
4. 解压此安装包，双击“TClash Install.cmd”。脚本会按系统架构安装或更新 TClash，
   检查采集器版本并应用本设备的新授权。若提示仍有旧采集器数据，核对第 2、3 步后重试。
5. 打开 TClash 的 CodeX Speed，使用 Codex 产生一条新请求。按 A 端设置的同步周期等待，
   然后在 A 端查看对应设备，核对新用量、设备名称和同步时间。

如果无法确认某个“Codex Usage Tracker”目录是不是旧 B，不要删除它；先核对 tracker.json 的 role。
tracker.json 含敏感令牌，请勿截图、上传或粘贴其完整内容。
