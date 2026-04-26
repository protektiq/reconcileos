use anyhow::{Result, anyhow};
use std::path::{Path, PathBuf};
use std::process::Command;

const MAX_ID_LEN: usize = 128;

pub fn resolve_repo_full_name() -> Result<String> {
    let cwd = std::env::current_dir()?;
    let repo_root = find_repo_root(&cwd).ok_or_else(|| anyhow!("not inside a git repository"))?;

    let configured = git_config_value(&repo_root, "GITHUB_REMOTE_URL")
        .or_else(|| git_config_value(&repo_root, "remote.origin.url"))
        .ok_or_else(|| anyhow!("missing GITHUB_REMOTE_URL or remote.origin.url in git config"))?;
    parse_repo_full_name(&configured)
}

pub fn validate_sha256_hex(raw: &str) -> Result<()> {
    let value = raw.trim();
    if value.len() != 64 || !value.bytes().all(|c| c.is_ascii_hexdigit()) {
        return Err(anyhow!("hash must be a 64-character sha256 hex string"));
    }
    Ok(())
}

pub fn validate_bot_id(raw: &str) -> Result<()> {
    let value = raw.trim();
    if value.is_empty() || value.len() > MAX_ID_LEN {
        return Err(anyhow!("bot_id is invalid"));
    }
    if !value
        .bytes()
        .all(|c| c.is_ascii_alphanumeric() || c == b'-' || c == b'_')
    {
        return Err(anyhow!("bot_id format is invalid"));
    }
    Ok(())
}

fn find_repo_root(start: &Path) -> Option<PathBuf> {
    let mut current = start.to_path_buf();
    loop {
        if current.join(".git").exists() {
            return Some(current);
        }
        if !current.pop() {
            return None;
        }
    }
}

fn git_config_value(repo_root: &Path, key: &str) -> Option<String> {
    let output = Command::new("git")
        .arg("-C")
        .arg(repo_root)
        .args(["config", "--get", key])
        .output()
        .ok()?;
    if !output.status.success() {
        return None;
    }
    let value = String::from_utf8_lossy(&output.stdout).trim().to_string();
    if value.is_empty() {
        return None;
    }
    Some(value)
}

fn parse_repo_full_name(remote: &str) -> Result<String> {
    let clean = remote.trim();
    if clean.is_empty() || clean.len() > 512 {
        return Err(anyhow!("git remote URL is invalid"));
    }

    if clean.starts_with("http://") || clean.starts_with("https://") {
        let url = url::Url::parse(clean).map_err(|_| anyhow!("git remote URL is invalid"))?;
        let path = url.path().trim_start_matches('/').trim_end_matches(".git");
        return validate_repo_path(path);
    }

    if let Some((_, path)) = clean.split_once(':') {
        return validate_repo_path(path.trim_end_matches(".git"));
    }

    validate_repo_path(clean.trim_end_matches(".git"))
}

fn validate_repo_path(path: &str) -> Result<String> {
    let parts: Vec<&str> = path.split('/').collect();
    if parts.len() != 2 {
        return Err(anyhow!(
            "unable to resolve repo_full_name from git remote URL"
        ));
    }
    let owner = parts[0].trim();
    let repo = parts[1].trim();
    if owner.is_empty() || repo.is_empty() || owner.len() > 255 || repo.len() > 255 {
        return Err(anyhow!("repo_full_name format is invalid"));
    }
    Ok(format!("{owner}/{repo}"))
}
