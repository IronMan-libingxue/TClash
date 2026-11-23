use chrono::Local;
use reqwest::{Client, StatusCode};

pub fn get_local_date_string() -> String {
    let now = Local::now();
    now.format("%Y-%m-%d %H:%M:%S").to_string()
}

pub fn country_code_to_emoji(country_code: &str) -> String {
    let uc = country_code.to_ascii_uppercase();

    match uc.len() {
        2 => {
            if rust_iso3166::from_alpha2(&uc).is_none() {
                return String::new();
            }
            alpha2_to_emoji(&uc)
        }
        3 => {
            // Regional indicators require the alpha-2 form.
            match rust_iso3166::from_alpha3(&uc) {
                Some(c) => {
                    let alpha2 = c.alpha2.to_ascii_uppercase();
                    alpha2_to_emoji(&alpha2)
                }
                None => String::new(),
            }
        }
        _ => String::new(),
    }
}

fn alpha2_to_emoji(alpha2: &str) -> String {
    let bytes = alpha2.as_bytes();
    let c1 = 0x1F1E6 + (bytes[0] as u32) - ('A' as u32);
    let c2 = 0x1F1E6 + (bytes[1] as u32) - ('A' as u32);
    char::from_u32(c1)
        .and_then(|x| char::from_u32(c2).map(|y| format!("{x}{y}")))
        .unwrap_or_default()
}

pub(crate) async fn get_text(client: &Client, url: &str) -> Option<String> {
    client.get(url).send().await.ok()?.text().await.ok()
}

pub(crate) async fn get_trace_location(client: &Client, url: &str) -> Option<String> {
    parse_trace(&get_text(client, url).await?).0
}

pub(crate) async fn get_trace(client: &Client, url: &str) -> (Option<String>, Option<String>) {
    get_text(client, url).await.map(|body| parse_trace(&body)).unwrap_or_default()
}

fn parse_trace(body: &str) -> (Option<String>, Option<String>) {
    let location = body.lines().find_map(|line| line.strip_prefix("loc=")).map(str::to_owned);
    let ip = body.lines().find_map(|line| line.strip_prefix("ip="))
        .and_then(|value| value.trim().parse::<std::net::IpAddr>().ok())
        .map(|value| value.to_string());
    (location, ip)
}

#[cfg(test)]
mod trace_tests {
    use super::parse_trace;

    #[test]
    fn trace_extracts_only_a_valid_reported_address() {
        assert_eq!(parse_trace("loc=JP\nip=203.0.113.7\n"), (Some("JP".into()), Some("203.0.113.7".into())));
        assert_eq!(parse_trace("loc=US\nip=not-an-ip\n"), (Some("US".into()), None));
        assert_eq!(parse_trace("loc=US\n"), (Some("US".into()), None));
        assert_eq!(parse_trace("ip=2001:db8::1\n"), (None, Some("2001:db8::1".into())));
    }
}

pub(crate) fn extract_quoted_field<'a>(body: &'a str, key: &str) -> Option<&'a str> {
    let (_, rest) = body.split_once(&format!(r#""{key}""#))?;
    let value = rest.split_once(':')?.1.trim_start();
    let value = value.strip_prefix('"')?;

    Some(value.split_once('"')?.0)
}

pub(crate) fn classify_restricted_status(status: StatusCode) -> Option<&'static str> {
    if matches!(
        status,
        StatusCode::FORBIDDEN | StatusCode::UNAVAILABLE_FOR_LEGAL_REASONS
    ) {
        Some("No")
    } else if !status.is_success() {
        Some("Failed")
    } else {
        None
    }
}
