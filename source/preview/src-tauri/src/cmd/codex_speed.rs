use super::{CmdResult, StringifyErr as _};

#[tauri::command]
#[specta::specta]
pub async fn get_codex_speed(window: String, cache: bool) -> CmdResult<String> {
    crate::tclash_collector::set_view_filter(&window, cache);
    tauri::async_runtime::spawn_blocking(move || crate::tclash_collector::view(&window, cache))
        .await
        .stringify_err()?
        .stringify_err()
}
