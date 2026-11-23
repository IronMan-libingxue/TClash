use super::{CmdResult, StringifyErr as _};
use crate::{config::{Config, PrfItem, profiles}, core::{handle::Handle, timer::Timer}, tclash_format, utils::help};
use anyhow::{Context as _, Result, bail, ensure};
use serde::{Deserialize, Serialize};
use std::collections::{HashMap, HashSet};

#[derive(Serialize, Deserialize)]
struct Record {
    item: PrfItem,
    content: std::string::String,
}

#[derive(Serialize, Deserialize)]
struct ProfilePackage {
    version: u8,
    root: std::string::String,
    records: Vec<Record>,
}

#[tauri::command]
#[specta::specta]
pub async fn export_tclash_profile(uid: std::string::String, destination: std::string::String) -> CmdResult<()> {
    export_profile(&uid, &destination).await.stringify_err()
}

async fn export_profile(uid: &str, destination: &str) -> Result<()> {
    let profiles = Config::profiles().await;
    let snapshot = profiles.latest_arc();
    let root = snapshot.get_item(uid)?.clone();
    ensure!(matches!(root.itype.as_deref(), Some("local" | "remote")), "not a subscription or local profile");
    let mut selected = vec![root.clone()];
    let mut seen = HashSet::from([uid.to_owned()]);
    if let Some(option) = &root.option {
        for linked in [&option.merge, &option.script, &option.rules, &option.proxies, &option.groups].into_iter().flatten() {
            if seen.insert(linked.to_string()) {
                selected.push(snapshot.get_item(linked.as_str())?.clone());
            }
        }
    }
    let mut records = Vec::with_capacity(selected.len());
    for item in selected {
        let content = item.read_file().await?.to_string();
        records.push(Record { item, content });
    }
    let package = ProfilePackage { version: 1, root: uid.to_owned(), records };
    let bytes = serde_json::to_vec(&package)?;
    tokio::fs::write(destination, tclash_format::pack_profile(&bytes)).await?;
    Ok(())
}

#[tauri::command]
#[specta::specta]
pub async fn import_tclash_profile(source: std::string::String) -> CmdResult<()> {
    import_profile(&source).await.stringify_err()
}

async fn import_profile(source: &str) -> Result<()> {
    let encoded = tokio::fs::read(source).await?;
    ensure!(encoded.len() <= 32 * 1024 * 1024, "TClash profile file is too large");
    let package: ProfilePackage = serde_json::from_slice(&tclash_format::unpack_profile(&encoded)?)?;
    let items = prepare_profile(package)?;
    profiles::profiles_import_package(items).await?;
    Timer::global().refresh().await?;
    Handle::refresh_clash();
    Ok(())
}

fn prepare_profile(package: ProfilePackage) -> Result<Vec<PrfItem>> {
    ensure!(package.version == 1 && !package.records.is_empty() && package.records.len() <= 6, "unsupported profile package");
    let mut remap = HashMap::new();
    let mut root_found = false;
    for record in &package.records {
        let id = record.item.uid.as_ref().context("profile entry is missing an ID")?;
        let kind = record.item.itype.as_deref().context("profile entry is missing a type")?;
        let prefix = match kind {
            "local" => "L", "remote" => "R", "merge" => "m", "script" => "s",
            "rules" => "r", "proxies" => "p", "groups" => "g",
            _ => bail!("unsupported profile entry type"),
        };
        if id.as_str() == package.root {
            ensure!(matches!(kind, "local" | "remote"), "invalid root profile");
            let _: serde_yaml_ng::Mapping = serde_yaml_ng::from_str(&record.content)?;
            root_found = true;
        } else {
            ensure!(!matches!(kind, "local" | "remote"), "unexpected profile in package");
        }
        ensure!(remap.insert(id.to_string(), help::get_uid(prefix)).is_none(), "duplicate profile entry");
    }
    ensure!(root_found, "root profile is missing");
    let mut items = Vec::with_capacity(package.records.len());
    for record in package.records {
        let mut item = record.item;
        let old = item.uid.as_ref().context("profile entry is missing an ID")?;
        let id = remap.get(old.as_str()).context("profile ID not mapped")?.clone();
        if let Some(option) = item.option.as_mut() {
            for linked in [&mut option.merge, &mut option.script, &mut option.rules, &mut option.proxies, &mut option.groups] {
                if let Some(old_link) = linked.as_ref() {
                    *linked = Some(remap.get(old_link.as_str()).context("linked configuration is missing")?.clone().into());
                }
            }
        }
        let extension = if item.itype.as_deref() == Some("script") { "js" } else { "yaml" };
        item.uid = Some(id.clone().into());
        item.file = Some(format!("{id}.{extension}").into());
        item.file_data = Some(record.content.into());
        items.push(item);
    }
    Ok(items)
}

#[cfg(test)]
mod tests {
    use super::{ProfilePackage, Record, prepare_profile};
    use crate::config::{PrfItem, PrfOption};

    #[test]
    fn profile_import_reconnects_scripts_and_rejects_missing_links() {
        let root = PrfItem {
            uid: Some("old-root".into()), itype: Some("local".into()),
            option: Some(PrfOption { script: Some("old-script".into()), ..Default::default() }),
            ..Default::default()
        };
        let script = PrfItem { uid: Some("old-script".into()), itype: Some("script".into()), ..Default::default() };
        let make = |records| ProfilePackage { version: 1, root: "old-root".into(), records };
        let record = |item, content: &str| Record { item, content: content.into() };
        let items = prepare_profile(make(vec![record(root.clone(), "proxies: []\n"), record(script, "function main() {}")])).unwrap();
        assert_ne!(items[0].uid.as_deref(), Some("old-root"));
        assert_eq!(items[0].option.as_ref().unwrap().script, items[1].uid);
        assert_eq!(items[1].file_data.as_deref(), Some("function main() {}"));
        assert!(prepare_profile(make(vec![record(root, "proxies: []\n")])).is_err());
    }
}
