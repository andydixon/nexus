//! Client for the licensing service.
//!
//! [`LicenseClient`] activates an installation through a customer-approved
//! browser flow, stores the signed seven-day activation lease, verifies it
//! offline on every start and renews it in the background. It never creates
//! windows or exits the host: it reports a [`LicenseState`] and the host decides.
//!
//! See `README.md` for an integration guide and `docs/protocol.md` for the wire
//! format.

mod client;
pub mod crypto;
pub mod device;
pub mod storage;

use std::sync::Arc;
use std::time::{Duration, SystemTime, UNIX_EPOCH};

pub use client::{ActivationSession, LicenseClient, LicenseStateReceiver};
pub use crypto::CertError;
pub use device::{DeviceError, DeviceKeyProvider, KeyHandle, SoftwareKeyProvider};
pub use ed25519_dalek::VerifyingKey;
pub use reqwest::Url;
pub use storage::{FileStorage, LicenseStorage, MemoryStorage, StorageError};

/// Commercial licensing state of this installation (spec §7.3).
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum LicenseState {
    Active { valid_until: SystemTime, offline_until: SystemTime, features: Vec<String> },
    Trial { expires_at: SystemTime, features: Vec<String> },
    ActivationRequired,
    ActivationPending,
    Scheduled { valid_from: SystemTime },
    Expired,
    TrialExpired,
    Revoked,
    DeviceDeauthorised,
    DeviceLimitReached,
    OfflineLeaseExpired,
    OnlineVerificationRequired,
    ClockIntegrityFailure,
    ServiceUnavailable,
}

impl LicenseState {
    /// Whether protected functionality may currently run.
    pub fn is_licensed(&self) -> bool {
        matches!(self, Self::Active { .. } | Self::Trial { .. })
    }
}

/// Operational failures, kept separate from commercial states.
#[derive(Debug, thiserror::Error)]
pub enum LicenseError {
    #[error("no network connection: {0}")]
    Network(String),
    #[error("DNS lookup failed: {0}")]
    Dns(String),
    #[error("request timed out")]
    Timeout,
    #[error("TLS failure: {0}")]
    Tls(String),
    #[error("licensing server error (HTTP {0})")]
    Server(u16),
    #[error("{code}: {detail}")]
    Problem { status: u16, code: String, detail: String },
    #[error("invalid certificate: {0}")]
    InvalidCertificate(#[from] CertError),
    #[error(transparent)]
    Storage(#[from] StorageError),
    #[error(transparent)]
    Device(#[from] DeviceError),
    #[error("protocol error: {0}")]
    Protocol(String),
    #[error("configuration error: {0}")]
    Config(String),
}

impl LicenseError {
    /// Failures worth retrying later without user action.
    pub fn is_transient(&self) -> bool {
        matches!(self, Self::Network(_) | Self::Dns(_) | Self::Timeout | Self::Server(_))
            || matches!(self, Self::Problem { code, .. } if code == "RATE_LIMITED")
    }
}

/// Source of wall-clock time; injectable for tests.
pub trait Clock: Send + Sync {
    fn now(&self) -> SystemTime;
}

pub struct SystemClock;

impl Clock for SystemClock {
    fn now(&self) -> SystemTime {
        SystemTime::now()
    }
}

pub struct ClientConfig {
    /// Product UUID as registered on the server.
    pub product_id: String,
    pub server_url: Url,
    /// Root public keys that sign the trust manifest.
    pub trust_anchors: Vec<VerifyingKey>,
    pub storage: Arc<dyn LicenseStorage>,
    pub key_provider: Arc<dyn DeviceKeyProvider>,
    pub clock: Arc<dyn Clock>,
    /// Device name shown to the customer; defaults to the host name.
    pub display_name: Option<String>,
}

impl ClientConfig {
    /// Defaults: file storage and a software key under the platform data dir.
    pub fn new(product_id: &str, server_url: &str, trust_anchors: Vec<VerifyingKey>) -> Result<Self, LicenseError> {
        let dir = |sub| FileStorage::default_dir(product_id, sub).ok_or_else(|| LicenseError::Config("no data directory".into()));
        Ok(Self {
            product_id: product_id.into(),
            server_url: server_url.parse().map_err(|e| LicenseError::Config(format!("server URL: {e}")))?,
            trust_anchors,
            storage: Arc::new(FileStorage::new(dir("state")?)),
            key_provider: Arc::new(SoftwareKeyProvider::new(Arc::new(FileStorage::new(dir("keys")?)))),
            clock: Arc::new(SystemClock),
            display_name: None,
        })
    }
}

fn unix(t: SystemTime) -> i64 {
    match t.duration_since(UNIX_EPOCH) {
        Ok(d) => d.as_secs() as i64,
        Err(e) => -(e.duration().as_secs() as i64),
    }
}

fn from_unix(s: i64) -> SystemTime {
    if s >= 0 {
        UNIX_EPOCH + Duration::from_secs(s as u64)
    } else {
        UNIX_EPOCH - Duration::from_secs(s.unsigned_abs())
    }
}
