use anyhow::{Context, Result, anyhow};
use std::env;

#[derive(Clone, Debug)]
pub struct Config {
    pub api_url: String,
    pub github_oauth_url: String,
    pub callback_port: u16,
}

impl Config {
    pub fn from_env() -> Result<Self> {
        let api_url = env::var("RECOS_API_URL")
            .unwrap_or_else(|_| "http://localhost:8080".to_string())
            .trim()
            .trim_end_matches('/')
            .to_string();
        let github_oauth_url = env::var("RECOS_GITHUB_OAUTH_URL")
            .unwrap_or_else(|_| "https://github.com/login/oauth/authorize".to_string())
            .trim()
            .to_string();
        let callback_port_raw =
            env::var("RECOS_CALLBACK_PORT").unwrap_or_else(|_| "9876".to_string());
        let callback_port = callback_port_raw
            .trim()
            .parse::<u16>()
            .with_context(|| "RECOS_CALLBACK_PORT must be a valid u16 port")?;

        if !(api_url.starts_with("http://") || api_url.starts_with("https://")) {
            return Err(anyhow!("RECOS_API_URL must start with http:// or https://"));
        }
        if !(github_oauth_url.starts_with("http://") || github_oauth_url.starts_with("https://")) {
            return Err(anyhow!(
                "RECOS_GITHUB_OAUTH_URL must start with http:// or https://"
            ));
        }

        Ok(Self {
            api_url,
            github_oauth_url,
            callback_port,
        })
    }
}
