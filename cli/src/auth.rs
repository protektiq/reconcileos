use crate::api_client::{ApiClient, OAuthExchangeRequest};
use crate::config::Config;
use anyhow::{Context, Result, anyhow};
use base64::Engine;
use keyring::Entry;
use serde_json::Value;
use std::io::{Read, Write};
use std::net::TcpListener;
use std::time::{Duration, Instant};
use url::Url;
use uuid::Uuid;

const KEYCHAIN_SERVICE: &str = "reconcileos.recos";
const KEYCHAIN_USERNAME: &str = "session";

pub struct SessionStore {
    entry: Entry,
}

impl SessionStore {
    pub fn new() -> Result<Self> {
        let entry = Entry::new(KEYCHAIN_SERVICE, KEYCHAIN_USERNAME)?;
        Ok(Self { entry })
    }

    pub fn store_access_token(&self, access_token: &str) -> Result<()> {
        let token = access_token.trim();
        if token.is_empty() || token.len() > 8192 {
            return Err(anyhow!("access token is invalid"));
        }
        self.entry.set_password(token)?;
        Ok(())
    }

    pub fn require_access_token(&self) -> Result<String> {
        let token = self
            .entry
            .get_password()
            .context("no saved session in keychain, run `recos login` first")?;
        if token.trim().is_empty() || token.len() > 8192 {
            return Err(anyhow!("stored session token is invalid"));
        }
        Ok(token)
    }
}

pub fn handle_login(
    config: &Config,
    client: &mut ApiClient,
    session_store: &SessionStore,
) -> Result<()> {
    let state = Uuid::new_v4().to_string();
    let callback_url = format!("http://localhost:{}/callback", config.callback_port);
    let auth_url = build_oauth_url(&config.github_oauth_url, &callback_url, &state)?;

    webbrowser::open(auth_url.as_str())?;
    println!(
        "Opened browser for GitHub OAuth. Waiting for callback on localhost:{}...",
        config.callback_port
    );

    let (code, returned_state) =
        wait_for_oauth_code(config.callback_port, Duration::from_secs(120))?;
    if state != returned_state {
        return Err(anyhow!("oauth state mismatch"));
    }

    let session = client.exchange_oauth_code(&OAuthExchangeRequest { code, state })?;
    session_store.store_access_token(&session.access_token)?;
    client.set_access_token(session.access_token.clone());

    let (github_username, org_name) = parse_identity_from_jwt(&session.access_token);
    println!("✓ Authenticated as {} ({})", github_username, org_name);
    Ok(())
}

fn build_oauth_url(base_oauth_url: &str, redirect_uri: &str, state: &str) -> Result<Url> {
    let mut url = Url::parse(base_oauth_url)?;
    {
        let mut pairs = url.query_pairs_mut();
        pairs.append_pair("redirect_uri", redirect_uri);
        pairs.append_pair("state", state);
    }
    Ok(url)
}

fn wait_for_oauth_code(port: u16, timeout: Duration) -> Result<(String, String)> {
    let listener = TcpListener::bind(("127.0.0.1", port))
        .with_context(|| format!("failed to bind localhost callback port {}", port))?;
    listener.set_nonblocking(false)?;
    let started = Instant::now();

    while started.elapsed() < timeout {
        let (mut stream, _) = listener.accept()?;
        let mut buffer = [0_u8; 4096];
        let read_bytes = stream.read(&mut buffer)?;
        if read_bytes == 0 {
            continue;
        }
        let request = String::from_utf8_lossy(&buffer[..read_bytes]);
        let first_line = request.lines().next().unwrap_or_default();
        let path = first_line.split_whitespace().nth(1).unwrap_or("/");
        let full_url = format!("http://localhost:{}{}", port, path);
        let parsed = Url::parse(&full_url)?;
        let code = parsed
            .query_pairs()
            .find(|(k, _)| k == "code")
            .map(|(_, v)| v.to_string())
            .unwrap_or_default();
        let state = parsed
            .query_pairs()
            .find(|(k, _)| k == "state")
            .map(|(_, v)| v.to_string())
            .unwrap_or_default();

        let response = if !code.is_empty() && !state.is_empty() {
            "HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\n\r\nAuthentication complete. You may close this window.\n"
        } else {
            "HTTP/1.1 400 Bad Request\r\nContent-Type: text/plain\r\n\r\nMissing OAuth code or state.\n"
        };
        let _ = stream.write_all(response.as_bytes());

        if !code.is_empty() && code.len() <= 2048 && !state.is_empty() && state.len() <= 2048 {
            return Ok((code, state));
        }
    }
    Err(anyhow!("timed out waiting for OAuth callback"))
}

fn parse_identity_from_jwt(token: &str) -> (String, String) {
    let fallback = ("unknown-user".to_string(), "unknown-org".to_string());
    let payload = token.split('.').nth(1).unwrap_or_default();
    if payload.is_empty() || payload.len() > 16_384 {
        return fallback;
    }
    let decoded = base64::engine::general_purpose::URL_SAFE_NO_PAD.decode(payload);
    let Ok(payload_bytes) = decoded else {
        return fallback;
    };
    let claims: Value = serde_json::from_slice(&payload_bytes).unwrap_or(Value::Null);
    let username = claims
        .get("user_metadata")
        .and_then(|v| v.get("github_login"))
        .and_then(Value::as_str)
        .unwrap_or("unknown-user")
        .to_string();
    let org = claims
        .get("app_metadata")
        .and_then(|v| v.get("org_name"))
        .and_then(Value::as_str)
        .or_else(|| {
            claims
                .get("user_metadata")
                .and_then(|v| v.get("github_org_slug"))
                .and_then(Value::as_str)
        })
        .unwrap_or("unknown-org")
        .to_string();
    (username, org)
}
