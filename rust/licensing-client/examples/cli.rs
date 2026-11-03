//! Minimal command-line host:
//! `cargo run --example cli -- <server_url> <product_id> <root_pub_hex> [status|activate [code]|trial|refresh|deactivate]`

use licensing_client::{ClientConfig, LicenseClient, VerifyingKey};

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    let args: Vec<String> = std::env::args().skip(1).collect();
    let [server, product, root, rest @ ..] = args.as_slice() else {
        eprintln!("usage: cli <server_url> <product_id> <root_pub_hex> [status|activate [code]|trial|refresh|deactivate]");
        std::process::exit(2);
    };
    let root: [u8; 32] = hex::decode(root)?.try_into().map_err(|_| "root key must be 32 bytes")?;
    let client = LicenseClient::new(ClientConfig::new(product, server, vec![VerifyingKey::from_bytes(&root)?])?).await?;
    println!("local state: {:?}", client.initialise().await?);

    match rest.first().map(String::as_str).unwrap_or("status") {
        "status" => {}
        cmd @ ("activate" | "trial") => {
            let session = if cmd == "trial" { client.request_trial().await? } else { client.begin_activation(rest.get(1).map(String::as_str)).await? };
            println!("Open {} (code {})", session.verification_uri_complete, session.user_code);
            println!("result: {:?}", client.finish_activation(session).await?);
        }
        "refresh" => println!("result: {:?}", client.refresh().await?),
        "deactivate" => {
            client.deactivate().await?;
            println!("deactivated");
        }
        other => return Err(format!("unknown command {other}").into()),
    }
    Ok(())
}
