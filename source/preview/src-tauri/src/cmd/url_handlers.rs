use super::{CmdResult, StringifyErr as _};

#[tauri::command]
#[specta::specta]
pub async fn restore_previous_url_handlers() -> CmdResult {
    crate::utils::url_handlers::restore().stringify_err()
}
