use serde::{Deserialize, Serialize};

use super::utils::{country_code_to_emoji, get_local_date_string};

#[derive(Debug, Clone, Serialize, Deserialize, specta::Type)]
pub struct UnlockItem {
    pub name: String,
    pub status: String,
    pub region: Option<String>,
    pub check_time: Option<String>,
    #[serde(default)]
    pub exit_ip: Option<String>,
    #[serde(default)]
    pub exit_ip_source: Option<String>,
}

impl UnlockItem {
    pub fn checked(name: &str, status: impl Into<String>, region: Option<String>) -> Self {
        Self {
            name: name.to_string(),
            status: status.into(),
            region,
            check_time: Some(get_local_date_string()),
            exit_ip: None,
            exit_ip_source: None,
        }
    }

    pub fn with_exit_ip(mut self, ip: Option<String>, source: &str) -> Self {
        self.exit_ip_source = ip.as_ref().map(|_| source.to_string());
        self.exit_ip = ip;
        self
    }

    pub fn checked_region(name: &str, status: impl Into<String>, country_code: &str) -> Self {
        Self::checked(name, status, Some(Self::region_label(country_code)))
    }

    pub fn region_label(country_code: &str) -> String {
        let emoji = country_code_to_emoji(country_code);
        format!("{emoji}{country_code}")
    }

    pub fn pending(name: &str) -> Self {
        Self {
            name: name.to_string(),
            status: "Pending".to_string(),
            region: None,
            check_time: None,
            exit_ip: None,
            exit_ip_source: None,
        }
    }
}
