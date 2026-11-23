# Windows x64 的原交付输入

| 位置 | 来源 |
| --- | --- |
| `2.5.11-Windows` | 2.5.11 Windows 交付用的 Inno Setup 输入，安装器版本为 2.5.11 |
| `2.5.12-Windows` | 2.5.12 Windows 交付用的 Inno Setup 输入，安装器版本为 2.5.12 |
| `Windows插件覆盖` | 本机交叉构建使用的 app_links 7.2.2 和 connectivity_plus 7.2.0 的 Windows 原生源码覆盖，附上游许可证 |
| `Windows交叉编译参数.txt` | 上次 macOS 主机交叉编译的参数记录，本机路径用占位符表示 |

构建时按编译参数准备工作目录和 xwin SDK。安装脚本中的原编译路径用于追溯，可切换到新的文件位置。原始 EXE 与 SHA‑256 在 Releases 对应版本下载。此前的安装及自动启动验收见[开发交接纪要](../../../docs/开发交接纪要.md)。
