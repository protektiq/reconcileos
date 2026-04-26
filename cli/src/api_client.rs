use crate::config::Config;
use anyhow::{Result, anyhow};
use reqwest::StatusCode;
use reqwest::blocking::Client;
use serde::{Deserialize, Serialize};
use serde_json::Value;
use std::time::Duration;

#[derive(Debug, Deserialize)]
pub struct RepoStatusResponse {
    pub open_cves: u64,
    pub active_bots: u64,
    pub last_execution_at: Option<String>,
    pub pending_reviews: u64,
    pub last_attestation_hash: Option<String>,
    pub last_attestation_at: Option<String>,
}

#[derive(Debug, Serialize)]
pub struct TriggerExecutionRequest {
    pub bot_id: String,
    pub repo_full_name: String,
    pub dry_run: bool,
}

#[derive(Debug, Deserialize)]
struct TriggerExecutionResponse {
    execution_id: String,
}

#[derive(Debug, Deserialize)]
pub struct ExecutionStatusResponse {
    pub id: String,
    pub status: String,
    pub result: Option<Value>,
    pub requires_review: bool,
    pub started_at: Option<String>,
    pub completed_at: Option<String>,
}

#[derive(Debug, Deserialize)]
pub struct VerifyAttestationResponse {
    pub records: Vec<AttestationRecord>,
}

#[derive(Debug, Deserialize, Clone)]
pub struct AttestationRecord {
    pub artifact_hash: String,
    pub rekor_log_index: Option<i64>,
    pub signed_at: Option<String>,
    pub slsa_predicate: Option<Value>,
    pub rekor_inclusion_proof: Option<Value>,
}

#[derive(Debug, Serialize)]
pub struct OAuthExchangeRequest {
    pub code: String,
    pub state: String,
}

#[derive(Debug, Deserialize)]
pub struct OAuthExchangeResponse {
    pub access_token: String,
}

pub struct ApiClient {
    config: Config,
    client: Client,
    access_token: Option<String>,
}

impl ApiClient {
    pub fn new(config: Config) -> Result<Self> {
        let client = Client::builder().timeout(Duration::from_secs(20)).build()?;
        Ok(Self {
            config,
            client,
            access_token: None,
        })
    }

    pub fn set_access_token(&mut self, token: String) {
        self.access_token = Some(token);
    }

    pub fn exchange_oauth_code(
        &self,
        request: &OAuthExchangeRequest,
    ) -> Result<OAuthExchangeResponse> {
        let response = self
            .client
            .post(format!("{}/auth/github/callback", self.config.api_url))
            .json(request)
            .send()?;

        if response.status() != StatusCode::OK {
            return Err(anyhow!(
                "oauth exchange failed with status {}",
                response.status()
            ));
        }
        Ok(response.json()?)
    }

    pub fn get_repo_status(&self, repo_full_name: &str) -> Result<RepoStatusResponse> {
        let token = self.require_token()?;
        let encoded_repo = urlencoding::encode(repo_full_name);
        let response = self
            .client
            .get(format!(
                "{}/api/v1/repos/{}/status",
                self.config.api_url, encoded_repo
            ))
            .bearer_auth(token)
            .send()?;
        if response.status() != StatusCode::OK {
            return Err(anyhow!("status request failed with {}", response.status()));
        }
        Ok(response.json()?)
    }

    pub fn trigger_execution(&self, body: &TriggerExecutionRequest) -> Result<String> {
        let token = self.require_token()?;
        let response = self
            .client
            .post(format!("{}/api/v1/executions/trigger", self.config.api_url))
            .bearer_auth(token)
            .json(body)
            .send()?;
        if response.status() != StatusCode::OK {
            return Err(anyhow!(
                "execution trigger failed with {}",
                response.status()
            ));
        }
        let payload: TriggerExecutionResponse = response.json()?;
        if payload.execution_id.trim().is_empty() {
            return Err(anyhow!("execution trigger response missing execution_id"));
        }
        Ok(payload.execution_id)
    }

    pub fn poll_execution_status(&self, execution_id: &str) -> Result<ExecutionStatusResponse> {
        loop {
            let current = self.get_execution_status(execution_id)?;
            match current.status.as_str() {
                "completed" | "failed" | "rejected" => return Ok(current),
                _ => std::thread::sleep(Duration::from_secs(2)),
            }
        }
    }

    pub fn get_execution_status(&self, execution_id: &str) -> Result<ExecutionStatusResponse> {
        let token = self.require_token()?;
        let response = self
            .client
            .get(format!(
                "{}/api/v1/executions/{}/status",
                self.config.api_url, execution_id
            ))
            .bearer_auth(token)
            .send()?;
        if response.status() != StatusCode::OK {
            return Err(anyhow!(
                "execution status request failed with {}",
                response.status()
            ));
        }
        Ok(response.json()?)
    }

    pub fn verify_attestation(&self, hash: &str) -> Result<VerifyAttestationResponse> {
        let response = self
            .client
            .post(format!(
                "{}/api/v1/attestations/verify",
                self.config.api_url
            ))
            .json(&serde_json::json!({ "artifact_hash": hash }))
            .send()?;
        if response.status() != StatusCode::OK {
            return Err(anyhow!("verify request failed with {}", response.status()));
        }
        Ok(response.json()?)
    }

    fn require_token(&self) -> Result<String> {
        self.access_token
            .clone()
            .ok_or_else(|| anyhow!("missing access token, run `recos login`"))
    }
}
