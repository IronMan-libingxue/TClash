use std::{collections::HashMap, sync::{Mutex, OnceLock}, time::{Duration, Instant}};

use serde_json::Value;
use tauri::{Manager as _, PhysicalPosition, WebviewUrl, WebviewWindow, WebviewWindowBuilder};

use crate::{core::{handle, tray::TRAY_ID}, tclash_collector, utils::speed::format_bytes_per_second};

const LABEL: &str = "codex-note";
const WIDTH: f64 = 370.0;
const HEIGHT: f64 = 230.0;

#[derive(Default)]
struct NoteState {
    pinned: bool,
    anchor: Option<(f64, f64)>,
    view: Option<Value>,
    download: Option<u64>,
}

static NOTE: OnceLock<Mutex<NoteState>> = OnceLock::new();

fn state() -> &'static Mutex<NoteState> { NOTE.get_or_init(|| Mutex::new(NoteState::default())) }

pub fn is_pinned() -> bool { state().lock().map(|value| value.pinned).unwrap_or(false) }

pub fn enter(x: f64, y: f64) {
    if let Ok(mut value) = state().lock() { value.anchor = Some((x, y)); }
    show_at(x, y);
}

pub fn remember_anchor(x: f64, y: f64) {
    if let Ok(mut value) = state().lock() { value.anchor = Some((x, y)); }
}

pub fn leave() {
    if !is_pinned() { hide(); }
}

pub fn toggle_pinned() {
    let (pinned, anchor) = match state().lock() {
        Ok(mut value) => { value.pinned = !value.pinned; (value.pinned, value.anchor) }
        Err(_) => return,
    };
    if pinned {
        if let Some((x, y)) = anchor { show_at(x, y); }
    } else { hide(); }
}

fn hide() {
    if let Some(window) = handle::Handle::app_handle().get_webview_window(LABEL) { let _ = window.hide(); }
}

fn show_at(x: f64, y: f64) {
    let app = handle::Handle::app_handle().clone();
    let main = app.clone();
    let _ = app.run_on_main_thread(move || {
        let window = match main.get_webview_window(LABEL) {
            Some(window) => window,
            None => {
                let builder = WebviewWindowBuilder::new(&main, LABEL, WebviewUrl::App("tray-note.html".into()))
                    .title("CodeX Speed")
                    .inner_size(WIDTH, HEIGHT)
                    .resizable(false)
                    .decorations(false)
                    .transparent(true)
                    .shadow(false)
                    .always_on_top(true)
                    .skip_taskbar(true)
                    .focused(false)
                    .visible(false)
                    .on_page_load(|window, payload| {
                        if payload.event() == tauri::webview::PageLoadEvent::Finished { render(&window); }
                    });
                match builder.build() { Ok(window) => window, Err(_) => return }
            }
        };
        #[cfg(target_os = "macos")]
        if let Ok(pointer) = window.ns_window() {
            if !pointer.is_null() {
                // The callback runs on the main thread and the window owns this pointer.
                unsafe { (&*(pointer as *const objc2_app_kit::NSWindow)).setAlphaValue(0.4); }
            }
        }
        if let Ok(Some(monitor)) = main.monitor_from_point(x, y) {
            let area = monitor.work_area();
            let scale = monitor.scale_factor();
            let width = WIDTH * scale;
            let height = HEIGHT * scale;
            let left = f64::from(area.position.x);
            let top = f64::from(area.position.y);
            let right = left + f64::from(area.size.width);
            let bottom = top + f64::from(area.size.height);
            let px = (x - width / 2.0).clamp(left, (right-width).max(left));
            let py = if y < top + f64::from(area.size.height)/2.0 { y + 16.0*scale } else { y-height-16.0*scale };
            let _ = window.set_position(PhysicalPosition::new(px.round() as i32, py.clamp(top, (bottom-height).max(top)).round() as i32));
        }
        render(&window);
        let _ = window.show();
    });
}

fn render(window: &WebviewWindow) {
    let payload = match state().lock() {
        Ok(value) => serde_json::json!({
            "view": value.view,
            "downloadText": value.download.map(format_bytes_per_second),
        }),
        Err(_) => return,
    };
    let _ = window.eval(&format!("window.renderCodexData?.({payload});"));
}

fn physical_interface(name: &str) -> bool {
    #[cfg(target_os = "macos")]
    { return name.strip_prefix("en").is_some_and(|number| !number.is_empty() && number.chars().all(|c| c.is_ascii_digit())); }
    #[cfg(not(target_os = "macos"))]
    {
        let name = name.to_ascii_lowercase();
        !["loopback", "virtual", "vethernet", "vmware", "hyper-v", "tun", "tap", "bluetooth", "wi-fi direct"].iter().any(|part| name.contains(part))
    }
}

pub fn start() {
    std::thread::spawn(|| {
        let mut networks = sysinfo::Networks::new_with_refreshed_list();
        let mut prior: HashMap<String, u64> = HashMap::new();
        let mut previous_at = Instant::now();
        let mut model_at = Instant::now() - Duration::from_secs(4);
        loop {
            if handle::Handle::global().is_exiting() { break; }
            networks.refresh(true);
            let now = Instant::now();
            let elapsed = now.duration_since(previous_at).as_secs_f64();
            let mut next = HashMap::new();
            let mut bytes = 0_u64;
            let mut comparable = 0;
            for (name, data) in networks.iter().filter(|(name, _)| physical_interface(name)) {
                let received = data.total_received();
                if let Some(old) = prior.get(name) && received >= *old {
                    bytes = bytes.saturating_add(received - old);
                    comparable += 1;
                }
                next.insert(name.clone(), received);
            }
            prior = next;
            previous_at = now;
            let download = (comparable > 0 && elapsed > 0.25).then(|| (bytes as f64 / elapsed) as u64);
            let refresh_model = model_at.elapsed() >= Duration::from_secs(4);
            let view = if refresh_model {
                model_at = Instant::now();
                tclash_collector::current_view().ok().and_then(|data| serde_json::from_str::<Value>(&data).ok())
            } else { None };
            let status = if let Ok(mut value) = state().lock() {
                value.download = download;
                if refresh_model { value.view = view; }
                let speed = value.view.as_ref().and_then(|v| v.get("speed"));
                (speed.and_then(|s| s.get("model")).and_then(Value::as_str).unwrap_or("").to_string(),
                 speed.and_then(|s| s.get("rate")).and_then(Value::as_f64), value.download)
            } else { (String::new(), None, None) };
            #[cfg(target_os = "macos")]
            if let Some(tray) = handle::Handle::app_handle().tray_by_id(TRAY_ID) {
                let _ = tray.with_inner_tray_icon(move |inner| {
                    if let Some(item) = inner.ns_status_item() {
                        crate::utils::tray_speed::set_codex_attributed_title(&item, &status.0, status.1, status.2);
                    }
                });
            }
            #[cfg(not(target_os = "macos"))]
            let _ = status;
            if let Some(window) = handle::Handle::app_handle().get_webview_window(LABEL) { render(&window); }
            std::thread::sleep(Duration::from_secs(1));
        }
    });
}
