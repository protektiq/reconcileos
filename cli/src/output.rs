use crate::api_client::{
    AttestationRecord, ExecutionStatusResponse, RepoStatusResponse, VerifyAttestationResponse,
};
use colored::Colorize;
use humantime::format_duration;
use serde_json::Value;
use std::time::Duration;

pub fn print_status(status: RepoStatusResponse) {
    println!("Open CVEs: {}", status.open_cves.to_string().yellow());
    println!("Active bots: {}", status.active_bots.to_string().green());
    println!(
        "Last execution: {}",
        format_time_ago(status.last_execution_at.as_deref())
    );
    println!(
        "Pending reviews: {}",
        status.pending_reviews.to_string().yellow()
    );
    let att_hash = status
        .last_attestation_hash
        .unwrap_or_else(|| "n/a".to_string());
    let att_time = format_time_ago(status.last_attestation_at.as_deref());
    println!("Last attestation: {} ({})", att_hash.cyan(), att_time);
}

pub fn print_run_result(result: &ExecutionStatusResponse) {
    println!("Execution: {}", result.id);
    println!(
        "Status: {}",
        if result.status == "completed" {
            result.status.green()
        } else {
            result.status.red()
        }
    );
    if let Some(started) = &result.started_at {
        println!("Started at: {started}");
    }
    if let Some(completed) = &result.completed_at {
        println!("Completed at: {completed}");
    }

    if let Some(payload) = &result.result {
        if let Some(diff) = payload.get("diff").and_then(Value::as_str) {
            println!("\nDiff:\n");
            print_colored_diff(diff);
        }
        if let Some(score) = payload.get("confidence_score") {
            println!("Confidence score: {}", score);
        }
        if let Some(recipes) = payload.get("recipes_applied") {
            println!("Recipes applied: {}", recipes);
        }
    }
    println!("Requires review: {}", result.requires_review);
}

pub fn print_verify_result(hash: &str, response: VerifyAttestationResponse) {
    let first = response
        .records
        .first()
        .cloned()
        .unwrap_or_else(empty_record);
    println!("Artifact: {}", first.artifact_hash.as_str().if_empty(hash));
    println!(
        "Rekor log index: {}",
        first
            .rekor_log_index
            .map(|v| v.to_string())
            .unwrap_or_else(|| "n/a".to_string())
    );
    println!(
        "Signed at: {}",
        first.signed_at.unwrap_or_else(|| "n/a".to_string())
    );
    let (bot_name, bot_version, ai_assisted) = parse_slsa_fields(first.slsa_predicate.as_ref());
    println!("Bot: {}@{}", bot_name, bot_version);
    println!("AI assisted: {}", if ai_assisted { "yes" } else { "no" });

    let proof_ok = first.rekor_inclusion_proof.is_some();
    if proof_ok {
        println!("Inclusion proof: {}", "VERIFIED ✓".green());
    } else {
        println!("Inclusion proof: {}", "INVALID ✗".red());
    }
}

trait IfEmpty {
    fn if_empty<'a>(&'a self, fallback: &'a str) -> &'a str;
}

impl IfEmpty for str {
    fn if_empty<'a>(&'a self, fallback: &'a str) -> &'a str {
        if self.trim().is_empty() {
            fallback
        } else {
            self
        }
    }
}

fn format_time_ago(timestamp: Option<&str>) -> String {
    let Some(raw) = timestamp else {
        return "n/a".to_string();
    };
    let Ok(parsed) = chrono::DateTime::parse_from_rfc3339(raw) else {
        return raw.to_string();
    };
    let now = chrono::Utc::now();
    let elapsed = now.signed_duration_since(parsed.with_timezone(&chrono::Utc));
    if elapsed.num_seconds() < 0 {
        return "just now".to_string();
    }
    let std_elapsed = Duration::from_secs(elapsed.num_seconds() as u64);
    format!("{} ago", format_duration(std_elapsed))
}

fn print_colored_diff(diff: &str) {
    for line in diff.lines() {
        if line.starts_with('+') {
            println!("{}", line.green());
        } else if line.starts_with('-') {
            println!("{}", line.red());
        } else if line.starts_with("@@") {
            println!("{}", line.yellow());
        } else {
            println!("{line}");
        }
    }
}

fn parse_slsa_fields(predicate: Option<&Value>) -> (String, String, bool) {
    let Some(pred) = predicate else {
        return (
            "unknown-bot".to_string(),
            "unknown-version".to_string(),
            false,
        );
    };
    let external_parameters = pred
        .get("predicate")
        .and_then(|p| p.get("buildDefinition"))
        .and_then(|d| d.get("externalParameters"));
    let bot_name = external_parameters
        .and_then(|e| e.get("bot_name"))
        .and_then(Value::as_str)
        .unwrap_or("unknown-bot")
        .to_string();
    let bot_version = external_parameters
        .and_then(|e| e.get("bot_version"))
        .and_then(Value::as_str)
        .unwrap_or("unknown-version")
        .to_string();
    let ai_assisted = external_parameters
        .and_then(|e| e.get("ai_assisted"))
        .and_then(Value::as_bool)
        .unwrap_or(false);
    (bot_name, bot_version, ai_assisted)
}

fn empty_record() -> AttestationRecord {
    AttestationRecord {
        artifact_hash: String::new(),
        rekor_log_index: None,
        signed_at: None,
        slsa_predicate: None,
        rekor_inclusion_proof: None,
    }
}
