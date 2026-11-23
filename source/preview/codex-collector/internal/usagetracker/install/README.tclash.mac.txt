TClash 远端设备重新接入（Mac）

本安装包含该设备的私有访问令牌，请只传送到指定设备，不要公开或发送到聊天。
新设备从接入时开始统计。原有 Codex 日志和 TClash 代理配置不需要删除。

1. 在 GitHub 上撤销该设备的旧访问令牌；不要撤销 A 端使用的令牌。
2. 完全退出 TClash Preview 和旧 B。若旧 B 设置了登录自启，请关闭其自启。
3. 在访达“前往文件夹”打开 ~/Library/Application Support/TClash Preview/，
   如存在 collector 文件夹，清理此旧 B 文件夹；不要删除其他代理配置。
4. 如存在 ~/Library/Application Support/Codex Usage Tracker/，先确认它是旧 B 的数据。
   只有确认为旧 B 时才能清理；如果是 A 端数据，保留该目录并不要在本机安装远端 B。
5. 解压此安装包，打开“TClash Install.command”。脚本会安装或更新应用并写入新的授权。
6. 打开 CodeX Speed，产生一条新的 Codex 请求，再按 A 的同步周期核对对应设备。

tracker.json 含敏感令牌，请勿截图、上传或粘贴其完整内容。不要删除 ~/.codex 中的原始日志。
