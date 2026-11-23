use crate::{
    config::{Config, IClashTemp, IProfiles, IVerge},
    core::{CoreManager, backup, proxy_control, proxy_control::SystemProxyStateUnknown},
    utils::{
        dirs::{PathBufExec as _, app_home_dir, local_backup_dir, verge_path},
        help,
    },
};
use anyhow::{Context as _, Result, anyhow, bail, ensure};
use chrono::Utc;
use clash_verge_logging::{Type, logging};
use reqwest_dav::list_cmd::ListFile;
use serde::Serialize;
use smartstring::alias::String;
use std::{collections::HashSet, io::{Cursor, Read as _}, path::{Component, Path, PathBuf}};
use tokio::fs;

#[derive(Debug, Serialize, specta::Type)]
pub struct LocalBackupFile {
    #[specta(type = std::string::String)]
    pub filename: String,
    #[specta(type = std::string::String)]
    pub path: String,
    #[specta(type = std::string::String)]
    pub last_modified: String,
    pub content_length: u64,
}

struct RestoreFile {
    relative: PathBuf,
    data: Vec<u8>,
}

fn checked_restore_files(raw: &[u8]) -> Result<Vec<RestoreFile>> {
    let decoded = crate::tclash_format::unpack_backup(raw)?;
    let mut archive = zip::ZipArchive::new(Cursor::new(decoded))?;
    ensure!(archive.len() <= 10_000, "backup has too many files");
    let mut files = Vec::new();
    let mut names = HashSet::new();
    let mut total = 0_u64;
    for index in 0..archive.len() {
        let mut entry = archive.by_index(index)?;
        let relative = entry.enclosed_name().context("unsafe backup path")?.to_path_buf();
        if entry.is_dir() {
            continue;
        }
        let components: Vec<_> = relative.components().collect();
        let allowed = match components.as_slice() {
            [Component::Normal(name)] => ["config.yaml", "verge.yaml", "profiles.yaml", "dns_config.yaml"].contains(&name.to_string_lossy().as_ref()),
            [Component::Normal(dir), Component::Normal(_)] => *dir == "profiles",
            _ => false,
        };
        ensure!(allowed && names.insert(relative.clone()), "unsupported or duplicate backup path");
        total = total.checked_add(entry.size()).context("backup size overflow")?;
        ensure!(total <= 256 * 1024 * 1024, "backup is too large");
        let mut data = Vec::new();
        entry.read_to_end(&mut data)?;
        files.push(RestoreFile { relative, data });
    }
    for required in ["config.yaml", "verge.yaml", "profiles.yaml"] {
        let file = files.iter().find(|file| file.relative == Path::new(required)).context("backup is missing a required configuration")?;
        let _: serde_yaml_ng::Mapping = serde_yaml_ng::from_slice(&file.data).context("backup configuration is invalid")?;
    }
    Ok(files)
}

fn rollback_restore(applied: &[(PathBuf, Option<Vec<u8>>)]) -> Result<()> {
    for (path, previous) in applied.iter().rev() {
        if let Some(previous) = previous {
            std::fs::write(path, previous)?;
        } else if path.exists() {
            std::fs::remove_file(path)?;
        }
    }
    Ok(())
}

fn apply_restore_files(root: &Path, files: Vec<RestoreFile>) -> Result<Vec<(PathBuf, Option<Vec<u8>>)>> {
    let mut applied = Vec::with_capacity(files.len());
    let result = (|| -> Result<()> {
        for file in files {
            let path = root.join(&file.relative);
            let parent = path.parent().context("backup destination has no parent")?;
            if parent != root && parent.exists() {
                ensure!(!std::fs::symlink_metadata(parent)?.file_type().is_symlink(), "profile directory is a symbolic link");
            }
            std::fs::create_dir_all(parent)?;
            let previous = match std::fs::symlink_metadata(&path) {
                Ok(metadata) => {
                    ensure!(metadata.is_file() && !metadata.file_type().is_symlink(), "backup destination is not a regular file");
                    Some(std::fs::read(&path)?)
                }
                Err(error) if error.kind() == std::io::ErrorKind::NotFound => None,
                Err(error) => return Err(error.into()),
            };
            applied.push((path.clone(), previous));
            std::fs::write(path, file.data)?;
        }
        Ok(())
    })();
    if let Err(error) = result {
        rollback_restore(&applied).context("failed to restore previous configuration")?;
        return Err(error);
    }
    Ok(applied)
}

async fn restore_backup_bytes(raw: &[u8], webdav_url: Option<String>, webdav_username: Option<String>, webdav_password: Option<String>) -> Result<()> {
    let files = checked_restore_files(raw)?;
    let _profile_write = crate::config::profiles::PROFILE_WRITE_LOCK.lock().await;
    let applied = apply_restore_files(&app_home_dir()?, files)?;
    if let Err(error) = finalize_restored_verge_config(webdav_url.clone(), webdav_username.clone(), webdav_password.clone()).await {
        rollback_restore(&applied).context("failed to restore previous configuration")?;
        if let Err(reload_error) = finalize_restored_verge_config(webdav_url, webdav_username, webdav_password).await {
            bail!("{error:#}; previous configuration reloading failed: {reload_error:#}");
        }
        return Err(error);
    }
    Ok(())
}

/// Reloads restored configs into memory while preserving WebDAV credentials.
async fn finalize_restored_verge_config(
    webdav_url: Option<String>,
    webdav_username: Option<String>,
    webdav_password: Option<String>,
) -> Result<()> {
    // A broken restored `verge.yaml` is a restore failure, not a reason to load defaults.
    let mut restored = help::read_yaml::<IVerge>(&verge_path()?).await?;
    restored.webdav_url = webdav_url;
    restored.webdav_username = webdav_username;
    restored.webdav_password = webdav_password;
    restored.save_file().await?;

    let restored_clash = IClashTemp::new().await;
    let clash_draft = Config::clash().await;
    clash_draft.edit_draft(|d| {
        *d = restored_clash.clone();
    });
    clash_draft.apply();

    let restored_profiles = IProfiles::new().await;
    let profiles_draft = Config::profiles().await;
    profiles_draft.edit_draft(|d| {
        *d = restored_profiles.clone();
    });
    profiles_draft.apply();

    let verge_draft = Config::verge().await;
    {
        // Hold the write lock so a concurrent patch cannot stage a draft the proxy write reads.
        let _config_write = Config::lock_config_write().await;
        verge_draft.edit_draft(|d| {
            *d = restored.clone();
        });
        verge_draft.apply();

        // Turn it off here; a failing core side effect below would otherwise skip this step.
        if !restored.enable_system_proxy.unwrap_or_default() {
            let result = async {
                let _lifecycle = CoreManager::global().lifecycle_lock.lock().await;
                proxy_control::apply().await?;
                proxy_control::refresh_guard().await
            }
            .await;
            if let Err(err) = result {
                logging!(error, Type::Backup, "Failed to turn the system proxy off: {err:#}");
                if SystemProxyStateUnknown::is_in(&err) {
                    return Err(err);
                }
            }
        }
    }

    // Run configuration side effects without rewriting the already-restored file.
    if let Err(err) = super::patch_verge(&restored, true).await {
        logging!(error, Type::Backup, "Failed to apply restored verge config: {err:#}");
        // Propagate unknown proxy state; ordinary side-effect failures stay logged.
        if SystemProxyStateUnknown::is_in(&err) {
            return Err(err);
        }
    }
    Ok(())
}

#[tracing::instrument(skip_all, level = "info")]
pub async fn create_backup_and_upload_webdav() -> Result<()> {
    let (file_name, temp_file_path) = backup::create_backup().await.map_err(|err| {
        logging!(error, Type::Backup, "Failed to create backup: {err:#}");
        err
    })?;

    if let Err(err) = backup::WebDavClient::global()
        .upload(temp_file_path.clone(), file_name)
        .await
    {
        logging!(error, Type::Backup, "Failed to upload to WebDAV: {err:#}");
        backup::WebDavClient::global().reset();
        return Err(err);
    }

    if let Err(err) = temp_file_path.remove_if_exists().await {
        logging!(warn, Type::Backup, "Failed to remove temp file: {err:#}");
    }

    Ok(())
}

pub async fn list_wevdav_backup() -> Result<Vec<ListFile>> {
    backup::WebDavClient::global().list().await.map_err(|err| {
        logging!(error, Type::Backup, "Failed to list WebDAV backup files: {err:#}");
        err
    })
}

pub async fn delete_webdav_backup(filename: String) -> Result<()> {
    let name = filename.to_string();
    backup::WebDavClient::global().delete(filename).await.map_err(|err| {
        logging!(
            error,
            Type::Backup,
            "Failed to delete WebDAV backup file {name}: {err:#}"
        );
        err
    })
}

#[tracing::instrument(skip_all, level = "info", fields(filename = %filename))]
pub async fn restore_webdav_backup(filename: String) -> Result<()> {
    let verge = Config::verge().await;
    let verge_data = verge.latest_arc();
    let webdav_url = verge_data.webdav_url.clone();
    let webdav_username = verge_data.webdav_username.clone();
    let webdav_password = verge_data.webdav_password.clone();

    let backup_storage_path = app_home_dir()
        .map_err(|e| anyhow::anyhow!("Failed to get app home dir: {e}"))?
        .join(filename.as_str());
    let name = filename.to_string();
    backup::WebDavClient::global()
        .download(filename, backup_storage_path.clone())
        .await
        .map_err(|err| {
            logging!(
                error,
                Type::Backup,
                "Failed to download WebDAV backup file {name}: {err:#}"
            );
            err
        })?;

    let raw = fs::read(&backup_storage_path).await?;
    let res = restore_backup_bytes(&raw, webdav_url, webdav_username, webdav_password).await;
    let _ = backup_storage_path.remove_if_exists().await;
    res
}

pub async fn create_local_backup() -> Result<()> {
    create_local_backup_with_namer(|name| name.to_string().into())
        .await
        .map(|_| ())
}

pub async fn create_local_backup_with_namer<F>(namer: F) -> Result<String>
where
    F: FnOnce(&str) -> String,
{
    let (file_name, temp_file_path) = backup::create_backup().await.map_err(|err| {
        logging!(error, Type::Backup, "Failed to create local backup: {err:#}");
        err
    })?;

    let backup_dir = local_backup_dir()?;
    let final_name = namer(file_name.as_str());
    let target_path = backup_dir.join(final_name.as_str());

    if let Err(err) = move_file(temp_file_path.clone(), target_path.clone()).await {
        logging!(
            error,
            Type::Backup,
            "Failed to move local backup file to {}: {err:#}",
            target_path.display()
        );
        if let Err(clean_err) = temp_file_path.remove_if_exists().await {
            logging!(
                warn,
                Type::Backup,
                "Failed to remove temp backup file after move error: {clean_err:#}"
            );
        }
        return Err(err);
    }

    Ok(final_name)
}

#[tracing::instrument(skip_all, level = "info", fields(source = %source))]
pub async fn import_local_backup(source: String) -> Result<String> {
    let source_path = PathBuf::from(source.as_str());
    if !source_path.exists() {
        return Err(anyhow!("Backup file not found: {source}"));
    }
    if !source_path.is_file() {
        return Err(anyhow!("Backup path is not a file: {source}"));
    }

    let ext = source_path
        .extension()
        .and_then(|ext| ext.to_str())
        .map(|ext| ext.to_ascii_lowercase())
        .unwrap_or_default();
    if ext != "zip" && ext != "tclash" {
        return Err(anyhow!("Only .zip and .tclash backup files are supported"));
    }
    let data = fs::read(&source_path).await?;
    checked_restore_files(&data)?;

    let file_name = source_path
        .file_name()
        .and_then(|name| name.to_str())
        .ok_or_else(|| anyhow!("Invalid backup file name"))?;

    let backup_dir = local_backup_dir()?;
    let target_path = backup_dir.join(file_name);

    if target_path == source_path {
        return Ok(file_name.to_string().into());
    }

    if let Some(parent) = target_path.parent() {
        fs::create_dir_all(parent).await?;
    }

    if target_path.exists() {
        return Err(anyhow!("Backup file already exists: {file_name}"));
    }

    fs::copy(&source_path, &target_path)
        .await
        .map_err(|err| anyhow!("Failed to import backup file: {err:#}"))?;

    Ok(file_name.to_string().into())
}

async fn move_file(from: PathBuf, to: PathBuf) -> Result<()> {
    if let Some(parent) = to.parent() {
        fs::create_dir_all(parent).await?;
    }

    match fs::rename(&from, &to).await {
        Ok(_) => Ok(()),
        Err(rename_err) => {
            // Rename can fail across filesystems; fall back to copy then remove.
            logging!(
                warn,
                Type::Backup,
                "Failed to rename backup file directly, fallback to copy/remove: {rename_err}"
            );
            fs::copy(&from, &to)
                .await
                .map_err(|err| anyhow!("Failed to copy backup file: {err:#}"))?;
            fs::remove_file(&from)
                .await
                .map_err(|err| anyhow!("Failed to remove temp backup file: {err:#}"))?;
            Ok(())
        }
    }
}

pub async fn list_local_backup() -> Result<Vec<LocalBackupFile>> {
    let backup_dir = local_backup_dir()?;
    if !backup_dir.exists() {
        return Ok(vec![]);
    }

    let mut backups = Vec::new();
    let mut dir = fs::read_dir(&backup_dir).await?;
    while let Some(entry) = dir.next_entry().await? {
        let path = entry.path();
        let metadata = entry.metadata().await?;
        if !metadata.is_file() {
            continue;
        }

        let file_name = match path.file_name().and_then(|name| name.to_str()) {
            Some(name) => name,
            None => continue,
        };
        let last_modified = metadata
            .modified()
            .map(|time| chrono::DateTime::<Utc>::from(time).to_rfc3339())
            .unwrap_or_default();
        backups.push(LocalBackupFile {
            filename: file_name.into(),
            path: path.to_string_lossy().into(),
            last_modified: last_modified.into(),
            content_length: metadata.len(),
        });
    }

    backups.sort_by(|a, b| b.filename.cmp(&a.filename));
    Ok(backups)
}

pub async fn delete_local_backup(filename: String) -> Result<()> {
    let backup_dir = local_backup_dir()?;
    let target_path = backup_dir.join(filename.as_str());
    if !target_path.exists() {
        logging!(debug, Type::Backup, "Local backup file not found: {}", filename);
        return Ok(());
    }
    target_path.remove_if_exists().await?;
    Ok(())
}

#[tracing::instrument(skip_all, level = "info", fields(filename = %filename))]
pub async fn restore_local_backup(filename: String) -> Result<()> {
    let backup_dir = local_backup_dir()?;
    let target_path = backup_dir.join(filename.as_str());
    if !target_path.exists() {
        return Err(anyhow!("Backup file not found: {}", filename));
    }

    let (webdav_url, webdav_username, webdav_password) = {
        let verge = Config::verge().await;
        let verge = verge.latest_arc();
        (
            verge.webdav_url.clone(),
            verge.webdav_username.clone(),
            verge.webdav_password.clone(),
        )
    };

    let raw = fs::read(&target_path).await?;
    restore_backup_bytes(&raw, webdav_url, webdav_username, webdav_password).await?;
    Ok(())
}

#[tracing::instrument(skip_all, level = "info", fields(filename = %filename, destination = %destination))]
pub async fn export_local_backup(filename: String, destination: String) -> Result<()> {
    let backup_dir = local_backup_dir()?;
    let source_path = backup_dir.join(filename.as_str());
    if !source_path.exists() {
        return Err(anyhow!("Backup file not found: {}", filename));
    }

    let dest_path = PathBuf::from(destination.as_str());
    if let Some(parent) = dest_path.parent() {
        fs::create_dir_all(parent).await?;
    }

    let raw = fs::read(&source_path).await?;
    let decoded = crate::tclash_format::unpack_backup(&raw)?;
    zip::ZipArchive::new(Cursor::new(&decoded))?;
    fs::write(&dest_path, crate::tclash_format::pack_backup(&decoded)).await?;
    Ok(())
}

#[cfg(test)]
mod tclash_restore_tests {
    use super::{apply_restore_files, checked_restore_files, rollback_restore};
    use std::io::{Cursor, Write as _};

    fn backup(verge: &[u8]) -> Vec<u8> {
        let mut zip = zip::ZipWriter::new(Cursor::new(Vec::new()));
        for (name, data) in [
            ("config.yaml", b"mode: rule\n".as_slice()),
            ("verge.yaml", verge),
            ("profiles.yaml", b"items: []\n".as_slice()),
            ("profiles/local.yaml", b"proxies: []\n".as_slice()),
            ("dns_config.yaml", b"enable: false\n".as_slice()),
        ] {
            zip.start_file(name, zip::write::SimpleFileOptions::default()).unwrap();
            zip.write_all(data).unwrap();
        }
        zip.finish().unwrap().into_inner()
    }

    #[test]
    fn checks_before_restore_and_can_roll_back_written_files() {
        let root = std::env::temp_dir().join(format!("tclash-restore-test-{}", nanoid::nanoid!(8)));
        std::fs::create_dir(&root).unwrap();
        std::fs::write(root.join("config.yaml"), "mode: direct\n").unwrap();
        assert!(checked_restore_files(&backup(b"invalid: [\n")).is_err());
        assert_eq!(std::fs::read_to_string(root.join("config.yaml")).unwrap(), "mode: direct\n");
        let encoded = crate::tclash_format::pack_backup(&backup(b"theme_mode: dark\n"));
        let files = checked_restore_files(&encoded).unwrap();
        let undo = apply_restore_files(&root, files).unwrap();
        assert_eq!(std::fs::read_to_string(root.join("config.yaml")).unwrap(), "mode: rule\n");
        rollback_restore(&undo).unwrap();
        assert_eq!(std::fs::read_to_string(root.join("config.yaml")).unwrap(), "mode: direct\n");
        assert!(!root.join("profiles/local.yaml").exists());
        std::fs::remove_dir_all(root).unwrap();
    }
}
