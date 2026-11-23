use super::{CmdResult, StringifyErr as _};

#[tauri::command]
#[specta::specta]
pub async fn get_codex_speed(window: String, cache: bool) -> CmdResult<String> {
    tauri::async_runtime::spawn_blocking(move || crate::tclash_collector::view(&window, cache))
        .await
        .stringify_err()?
        .stringify_err()
}
