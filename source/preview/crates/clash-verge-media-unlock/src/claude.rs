use reqwest::Client;

use crate::utils::get_trace;

use super::UnlockItem;

pub(crate) const CLAUDE_NAME: &str = "Claude";

const BLOCKED_CODES: &[&str] = &["AF", "BY", "CN", "CU", "HK", "IR", "KP", "MO", "RU", "SY"];

pub(super) async fn check_claude(client: &Client) -> UnlockItem {
    let (location, ip) = get_trace(client, "https://claude.ai/cdn-cgi/trace").await;
    let code = location.map(|code| code.trim().to_ascii_uppercase());

    let Some(code) = code else {
        return UnlockItem::checked(CLAUDE_NAME, "Failed", None).with_exit_ip(ip, "claude.ai");
    };

    let status = if BLOCKED_CODES.contains(&code.as_str()) {
        "No"
    } else {
        "Yes"
    };

    UnlockItem::checked_region(CLAUDE_NAME, status, &code).with_exit_ip(ip, "claude.ai")
}
