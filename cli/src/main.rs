mod api_client;
mod auth;
mod config;
mod git;
mod output;

use anyhow::Result;
use clap::{Parser, Subcommand};

#[derive(Debug, Parser)]
#[command(name = "recos", about = "ReconcileOS command-line interface")]
struct Cli {
    #[command(subcommand)]
    command: Commands,
}

#[derive(Debug, Subcommand)]
enum Commands {
    /// Authenticate to ReconcileOS
    Login,
    /// Show reconciliation status for current repo
    Status,
    /// Execute a bot against current repo
    Run {
        bot_id: String,
        #[arg(long, default_value_t = false)]
        live: bool,
    },
    /// Verify an artifact attestation chain
    Verify { hash: String },
    /// Publish a bot to marketplace (Phase 3)
    Publish,
}

fn main() {
    if let Err(err) = run() {
        eprintln!("error: {err:#}");
        std::process::exit(1);
    }
}

fn run() -> Result<()> {
    let cli = Cli::parse();
    let config = config::Config::from_env()?;
    let session_store = auth::SessionStore::new()?;
    let mut client = api_client::ApiClient::new(config.clone())?;

    match cli.command {
        Commands::Login => auth::handle_login(&config, &mut client, &session_store)?,
        Commands::Status => {
            let token = session_store.require_access_token()?;
            client.set_access_token(token);
            let repo = git::resolve_repo_full_name()?;
            let status = client.get_repo_status(&repo)?;
            output::print_status(status);
        }
        Commands::Run { bot_id, live } => {
            git::validate_bot_id(&bot_id)?;
            let token = session_store.require_access_token()?;
            client.set_access_token(token);
            let repo = git::resolve_repo_full_name()?;
            let execution_id = client.trigger_execution(&api_client::TriggerExecutionRequest {
                bot_id,
                repo_full_name: repo,
                dry_run: !live,
            })?;
            let result = client.poll_execution_status(&execution_id)?;
            output::print_run_result(&result);

            if result.status == "failed" {
                std::process::exit(1);
            }
        }
        Commands::Verify { hash } => {
            git::validate_sha256_hex(&hash)?;
            let result = client.verify_attestation(&hash)?;
            output::print_verify_result(&hash, result);
        }
        Commands::Publish => {
            println!("publish is a Phase 3 feature and is currently stubbed");
        }
    }

    Ok(())
}
