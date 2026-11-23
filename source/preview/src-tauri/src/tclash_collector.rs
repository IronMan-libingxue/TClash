use std::{
    io::{BufRead, BufReader, Write},
    path::PathBuf,
    process::{Child, ChildStdin, ChildStdout, Command, Stdio},
    sync::{Mutex, OnceLock},
};

struct Collector {
    child: Child,
    input: ChildStdin,
    output: BufReader<ChildStdout>,
}

static COLLECTOR: OnceLock<Mutex<Option<Collector>>> = OnceLock::new();

fn executable() -> anyhow::Result<PathBuf> {
    let name = format!("tclash-collector{}", std::env::consts::EXE_SUFFIX);
    let installed = std::env::current_exe()?.with_file_name(&name);
    if installed.is_file() {
        return Ok(installed);
    }
    #[cfg(debug_assertions)]
    {
        let triple = match (std::env::consts::OS, std::env::consts::ARCH) {
            ("macos", "aarch64") => "aarch64-apple-darwin",
            ("macos", "x86_64") => "x86_64-apple-darwin",
            ("windows", "aarch64") => "aarch64-pc-windows-msvc",
            ("windows", "x86_64") => "x86_64-pc-windows-msvc",
            _ => anyhow::bail!("unsupported collector development platform"),
        };
        let path = PathBuf::from(env!("CARGO_MANIFEST_DIR"))
            .join("sidecar")
            .join(format!("tclash-collector-{triple}{}", std::env::consts::EXE_SUFFIX));
        if path.is_file() {
            return Ok(path);
        }
    }
    anyhow::bail!("Codex collector is missing from the application")
}

fn spawn() -> anyhow::Result<Collector> {
    let mut command = Command::new(executable()?);
    command.stdin(Stdio::piped()).stdout(Stdio::piped()).stderr(Stdio::null());
    #[cfg(windows)]
    {
        use std::os::windows::process::CommandExt as _;
        command.creation_flags(0x0800_0000);
    }
    let mut child = command.spawn()?;
    let input = child.stdin.take().ok_or_else(|| anyhow::anyhow!("collector input unavailable"))?;
    let output = child.stdout.take().ok_or_else(|| anyhow::anyhow!("collector output unavailable"))?;
    Ok(Collector { child, input, output: BufReader::new(output) })
}

pub fn start() -> anyhow::Result<()> {
    let mut guard = COLLECTOR.get_or_init(|| Mutex::new(None)).lock().map_err(|e| anyhow::anyhow!(e.to_string()))?;
    if guard.is_none() {
        *guard = Some(spawn()?);
    }
    Ok(())
}

pub fn view(window: &str, cache: bool) -> anyhow::Result<String> {
    if window.len() > 256 {
        anyhow::bail!("invalid period");
    }
    start()?;
    let mut guard = COLLECTOR.get().ok_or_else(|| anyhow::anyhow!("collector unavailable"))?
        .lock().map_err(|e| anyhow::anyhow!(e.to_string()))?;
    let collector = guard.as_mut().ok_or_else(|| anyhow::anyhow!("collector unavailable"))?;
    let request = serde_json::json!({ "window": window, "cache": cache });
    let result = (|| -> anyhow::Result<String> {
        writeln!(collector.input, "{request}")?;
        collector.input.flush()?;
        let mut line = String::new();
        if collector.output.read_line(&mut line)? == 0 {
            anyhow::bail!("collector stopped");
        }
        let _: serde_json::Value = serde_json::from_str(&line)?;
        Ok(line)
    })();
    if result.is_err() {
        if let Some(mut failed) = guard.take() {
            let _ = failed.child.kill();
            let _ = failed.child.wait();
        }
    }
    result
}

pub fn stop() {
    if let Some(lock) = COLLECTOR.get()
        && let Ok(mut guard) = lock.lock()
        && let Some(mut collector) = guard.take()
    {
        let _ = collector.child.kill();
        let _ = collector.child.wait();
    }
}
