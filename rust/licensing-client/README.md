# licensing-client

Rust client for the licensing service (protocol: `../../docs/protocol.md`).
It activates an installation through a customer-approved browser flow, keeps a
signed activation lease (at most seven days), verifies it offline on every
start and renews it in the background. It never opens windows or exits your
process: it reports a `LicenseState` and your application decides what to do.

## Integration

```toml
[dependencies]
licensing-client = { path = "client/rust" }
```

```rust
use licensing_client::{ClientConfig, LicenseClient, LicenseState, VerifyingKey};

// Production root (root-2026-11): e3f1dbfac9092864bac047cbad9c9155998e7acbfe7c42c563afb69b30c5bbed
// Embed it in your binary; issuer keys rotate underneath it without a rebuild.
const ROOT: [u8; 32] = [/* ... */];

let cfg = ClientConfig::new(PRODUCT_ID, "https://licensing.uncomplex.co.uk", vec![VerifyingKey::from_bytes(&ROOT)?])?;
let licensing = LicenseClient::new(cfg).await?;

// Offline: loads and verifies the cached lease.
let state = licensing.initialise().await?;
// Online: renews now and about every 24 hours.
licensing.spawn_renewal();

// React to every later change, not just the initial state.
let mut rx = licensing.subscribe();
tokio::spawn(async move {
    while rx.changed().await.is_ok() {
        let s = rx.borrow().clone();
        // update UI; if !s.is_licensed(), let the user save work, then restrict features
    }
});

if !state.is_licensed() {
    let session = licensing.begin_activation(None).await?; // or request_trial()
    show(&session.verification_uri_complete, &session.user_code); // link carries the code
    licensing.finish_activation(session).await?; // polls until approved, then verifies the lease
}

if licensing.has_feature("pro") { /* ... */ }
```

`cargo run --example cli -- <server_url> <product_id> <root_pub_hex> [status|activate [code]|trial|refresh|deactivate]`
exercises the whole flow from a terminal.

## States

| State | Meaning | Typical host action |
|---|---|---|
| `Active` / `Trial` | Valid lease; `features` are entitled | Run normally |
| `ActivationRequired` | No usable activation | Offer activation or trial |
| `ActivationPending` | Waiting for browser approval | Show code and URL |
| `OfflineLeaseExpired` | Seven days without renewal | Ask the user to reconnect |
| `ServiceUnavailable` | Lease expired and server unreachable | Retry later |
| `Expired` / `TrialExpired` | Commercial entitlement ended | Read-only, offer renewal |
| `Revoked` | Licence suspended or revoked | Contact support |
| `DeviceDeauthorised` | This installation was removed | Reactivate |
| `DeviceLimitReached` | No free machine slot | Manage devices in the portal |
| `ClockIntegrityFailure` | Clock moved back over 10 minutes | Fix the clock, then `refresh()` |
| `OnlineVerificationRequired` | Cached lease cannot be verified (e.g. new issuer key) | `refresh()` |
| `Scheduled` | Licence starts later | Wait |

Operational failures (network, DNS, timeout, TLS, server, malformed response,
storage, device key) are `LicenseError`s, separate from these states.
`LicenseError::is_transient()` tells you whether retrying later makes sense.

## Storage

State lives in `<data dir>/licensing/<product_id>/state/state` and the device key
in `.../keys/` (0600 on Unix). Supply your own `LicenseStorage`,
`DeviceKeyProvider` or `Clock` through `ClientConfig` if you need to.

## Limitations

- The only key provider is `SoftwareKeyProvider`, reported as `software`
  (lower assurance). TPM, CNG and Secure Enclave providers are not implemented.
- Local enforcement can be bypassed by anyone willing to modify your binary.
  Keep entitlement checks close to the protected operation and verify anything
  server-side that you can.
- Clock-rollback detection catches casual changes only; deleting the state
  directory or restoring a VM snapshot resets it.
- On Linux the strongest hardware identifiers (`product_uuid`, `board_serial`)
  are usually root-only, so recognition after a reinstall may need the
  customer to confirm the machine in the portal.
- Several processes sharing one installation write state atomically but
  without a lock (last writer wins).
