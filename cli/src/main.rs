use clap::Parser;

#[derive(Debug, Parser)]
#[command(name = "recos", about = "ReconcileOS command-line interface")]
struct Cli {
    #[arg(short, long, default_value_t = false, help = "Enable verbose output")]
    verbose: bool,
}

fn main() {
    let _cli = Cli::parse();
}
