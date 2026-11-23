//! Remember the previous macOS URL handlers when the installed app first runs.

#[cfg(target_os = "macos")]
mod macos {
    use anyhow::{Context as _, Result, bail};
    use core_foundation::{
        base::TCFType as _,
        string::{CFString, CFStringRef},
    };
    use serde::{Deserialize, Serialize};
    use std::{
        collections::BTreeMap,
        io::Write as _,
        os::unix::fs::OpenOptionsExt as _,
        path::{Path, PathBuf},
    };

    const OUR_ID: &str = "local.tclash.preview";
    const SCHEMES: [&str; 2] = ["clash", "clash-verge"];

    #[link(name = "CoreServices", kind = "framework")]
    unsafe extern "C" {
        fn LSCopyDefaultHandlerForURLScheme(scheme: CFStringRef) -> CFStringRef;
        fn LSSetDefaultHandlerForURLScheme(scheme: CFStringRef, handler: CFStringRef) -> i32;
    }

    #[derive(Serialize, Deserialize)]
    struct PreviousHandlers {
        previous: BTreeMap<String, Option<String>>,
        restored: bool,
    }

    fn current(scheme: &str) -> Option<String> {
        let scheme = CFString::new(scheme);
        // Launch Services returns an owned CFString, or null if there is no handler.
        let value = unsafe { LSCopyDefaultHandlerForURLScheme(scheme.as_concrete_TypeRef()) };
        if value.is_null() {
            None
        } else {
            Some(unsafe { CFString::wrap_under_create_rule(value) }.to_string())
        }
    }

    fn set(scheme: &str, handler: &str) -> Result<()> {
        let scheme_value = CFString::new(scheme);
        let handler_value = CFString::new(handler);
        let result = unsafe {
            LSSetDefaultHandlerForURLScheme(scheme_value.as_concrete_TypeRef(), handler_value.as_concrete_TypeRef())
        };
        if result != 0 {
            bail!("无法设置 {scheme} 订阅链接处理程序（系统返回 {result}）");
        }
        if current(scheme).as_deref() != Some(handler) {
            bail!("系统没有确认 {scheme} 链接的默认程序，请在系统设置中检查");
        }
        Ok(())
    }

    fn record_path() -> Result<PathBuf> {
        Ok(crate::utils::dirs::app_home_dir()?.join("previous-url-handlers.json"))
    }

    fn installed(exe: &Path, home: &Path) -> bool {
        exe.starts_with(home.join("Applications/TClash Preview.app/Contents/MacOS"))
            || exe.starts_with("/Applications/TClash Preview.app/Contents/MacOS")
    }

    pub fn install_if_in_applications() -> Result<()> {
        let exe = std::env::current_exe()?;
        let home = dirs::home_dir().context("无法读取用户目录")?;
        if !installed(&exe, &home) {
            return Ok(());
        }
        let path = record_path()?;
        if path.exists() {
            return Ok(());
        }
        let record = PreviousHandlers {
            previous: SCHEMES
                .into_iter()
                .map(|scheme| (scheme.to_owned(), current(scheme)))
                .collect(),
            restored: false,
        };
        std::fs::create_dir_all(path.parent().context("缺少保存目录")?)?;
        let mut file = std::fs::OpenOptions::new()
            .write(true)
            .create_new(true)
            .mode(0o600)
            .open(&path)?;
        file.write_all(&serde_json::to_vec_pretty(&record)?)?;
        file.sync_all()?;
        let mut applied = Vec::new();
        for scheme in SCHEMES {
            if let Err(error) = set(scheme, OUR_ID) {
                for previous_scheme in applied {
                    if let Some(Some(previous)) = record.previous.get(previous_scheme) {
                        let _ = set(previous_scheme, previous);
                    }
                }
                return Err(error);
            }
            applied.push(scheme);
        }
        Ok(())
    }

    pub fn restore() -> Result<()> {
        let path = record_path()?;
        let mut record: PreviousHandlers =
            serde_json::from_slice(&std::fs::read(&path).context("没有找到安装前的订阅链接设置")?)?;
        for scheme in SCHEMES {
            if current(scheme).as_deref() != Some(OUR_ID) {
                continue;
            }
            match record.previous.get(scheme).and_then(Option::as_deref) {
                Some(previous) if previous != OUR_ID => set(scheme, previous)?,
                _ => bail!("{scheme} 没有可恢复的旧程序，请在系统设置中选择默认程序"),
            }
        }
        record.restored = true;
        std::fs::write(path, serde_json::to_vec_pretty(&record)?)?;
        Ok(())
    }

    #[cfg(test)]
    mod tests {
        use super::installed;
        use std::path::Path;
        #[test]
        fn preview_cannot_change_the_default_handler() {
            let home = Path::new("/Users/example");
            assert!(!installed(
                Path::new("/tmp/TClash Preview.app/Contents/MacOS/clash-verge"),
                home
            ));
            assert!(installed(
                Path::new("/Users/example/Applications/TClash Preview.app/Contents/MacOS/clash-verge"),
                home
            ));
            assert!(installed(
                Path::new("/Applications/TClash Preview.app/Contents/MacOS/clash-verge"),
                home
            ));
        }
    }
}

#[cfg(target_os = "macos")]
pub use macos::{install_if_in_applications, restore};

#[cfg(target_os = "windows")]
mod windows {
    use anyhow::{Context as _, Result, bail};
    use std::{fs, os::windows::process::CommandExt as _, path::PathBuf, process::Command};
    use winreg::{RegKey, enums::HKEY_CURRENT_USER};
    const SCHEMES: [&str; 2] = ["clash", "clash-verge"];
    fn dir() -> Result<PathBuf> {
        Ok(crate::utils::dirs::app_home_dir()?.join("previous-url-handlers"))
    }

    pub fn prepare_windows_registration() -> Result<bool> {
        let dir = dir()?;
        fs::create_dir_all(&dir)?;
        if dir.join("registered").exists() {
            return Ok(false);
        }
        if !dir.join("prepared").exists() {
            for scheme in SCHEMES {
                let status = Command::new("reg.exe")
                    .args(["export", &format!(r"HKCU\Software\Classes\{scheme}")])
                    .arg(dir.join(format!("{scheme}.reg")))
                    .arg("/y")
                    .creation_flags(0x08000000)
                    .status()?;
                if !status.success() {
                    fs::write(dir.join(format!("{scheme}.absent")), b"")?;
                }
            }
            fs::write(dir.join("prepared"), b"")?;
        }
        Ok(true)
    }

    pub fn finish_windows_registration() -> Result<()> {
        fs::write(dir()?.join("registered"), b"")?;
        Ok(())
    }

    pub fn restore() -> Result<()> {
        let dir = dir()?;
        if !dir.join("prepared").exists() {
            bail!("没有找到安装前的订阅链接设置");
        }
        let executable = std::env::current_exe()?.to_string_lossy().to_lowercase();
        let hkcu = RegKey::predef(HKEY_CURRENT_USER);
        for scheme in SCHEMES {
            let base = format!(r"Software\Classes\{scheme}");
            let command: String = hkcu
                .open_subkey(format!(r"{base}\Shell\Open\Command"))
                .ok()
                .and_then(|key| key.get_value("").ok())
                .unwrap_or_default();
            if !command.to_lowercase().contains(&executable) {
                continue;
            }
            let saved = dir.join(format!("{scheme}.reg"));
            if saved.exists() {
                let status = Command::new("reg.exe")
                    .arg("import")
                    .arg(&saved)
                    .creation_flags(0x08000000)
                    .status()?;
                if !status.success() {
                    bail!("无法恢复 {scheme} 的默认程序");
                }
            } else if dir.join(format!("{scheme}.absent")).exists() {
                hkcu.delete_subkey_all(&base).context("无法恢复原来的订阅链接设置")?;
            } else {
                bail!("没有找到 {scheme} 的原设置");
            }
        }
        fs::write(dir.join("registered"), b"restored")?;
        Ok(())
    }
}

#[cfg(target_os = "windows")]
pub use windows::{finish_windows_registration, prepare_windows_registration, restore};

#[cfg(not(any(target_os = "macos", target_os = "windows")))]
pub fn restore() -> anyhow::Result<()> {
    anyhow::bail!("当前系统的订阅链接恢复功能尚未验证，请在系统设置中选择默认程序")
}
