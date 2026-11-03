use std::fmt;
use std::sync::Arc;
use std::time::{Duration, SystemTime};

use base64::{engine::general_purpose::URL_SAFE_NO_PAD as B64, Engine};
use rand_core::{OsRng, RngCore};
use serde::{de::DeserializeOwned, Deserialize, Serialize};
use tokio::sync::{watch, Mutex};

use crate::crypto::{self, CertError, LeaseClaims, LeaseExpectations, Manifest};
use crate::device::{self, DeviceError, KeyHandle};
use crate::{from_unix, unix, ClientConfig, LicenseError, LicenseState, Url};

const STATE_KEY: &str = "state";
/// Backward clock movement tolerated before online revalidation is required.
const CLOCK_TOLERANCE_SECS: i64 = 600;
const MAX_RESPONSE_BYTES: usize = 256 * 1024;
const RENEW_EVERY_SECS: u64 = 23 * 3600; // plus up to 2h jitter: ~24h ± 1h
const MAX_BACKOFF_SECS: u64 = 6 * 3600;

pub type LicenseStateReceiver = watch::Receiver<LicenseState>;

/// Outcome the server reported authoritatively; survives restarts.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
enum Terminal {
    Revoked,
    Deauthorised,
    Expired,
    TrialExpired,
}

impl Terminal {
    fn state(self) -> LicenseState {
        match self {
            Self::Revoked => LicenseState::Revoked,
            Self::Deauthorised => LicenseState::DeviceDeauthorised,
            Self::Expired => LicenseState::Expired,
            Self::TrialExpired => LicenseState::TrialExpired,
        }
    }
}

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
struct Persisted {
    activation_id: Option<String>,
    lease: Option<String>,
    manifest: Option<String>,
    key: Option<String>,
    /// Latest trusted time seen (Unix seconds), for rollback detection.
    high_water: i64,
    terminal: Option<Terminal>,
}

/// An activation awaiting customer approval in the browser.
#[derive(Clone)]
pub struct ActivationSession {
    pub intent_id: String,
    /// Code the customer enters at `verification_uri`.
    pub user_code: String,
    pub verification_uri: String,
    /// `verification_uri` with the code filled in; open this so the customer need not type it.
    pub verification_uri_complete: String,
    pub expires_at: SystemTime,
    pub poll_interval: Duration,
    secret: String,
    key: KeyHandle,
}

impl fmt::Debug for ActivationSession {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.debug_struct("ActivationSession")
            .field("intent_id", &self.intent_id)
            .field("user_code", &self.user_code)
            .field("verification_uri", &self.verification_uri)
            .field("verification_uri_complete", &self.verification_uri_complete)
            .field("expires_at", &self.expires_at)
            .finish_non_exhaustive()
    }
}

/// Thread-safe licence manager; cheap to clone.
#[derive(Clone)]
pub struct LicenseClient {
    inner: Arc<Inner>,
}

struct Inner {
    cfg: ClientConfig,
    base: Url,
    http: reqwest::Client,
    /// Serialises state changes (and the network calls that lead to them).
    lock: Mutex<()>,
    tx: watch::Sender<LicenseState>,
}

#[derive(Deserialize)]
struct IntentResponse {
    intent_id: String,
    intent_secret: String,
    user_code: String,
    verification_uri: String,
    #[serde(default)] // absent from servers before this field existed
    verification_uri_complete: String,
    expires_in: u64,
    poll_interval: u64,
}

#[derive(Deserialize)]
struct StatusResponse {
    status: String,
}

#[derive(Deserialize)]
struct ChallengeResponse {
    challenge: String,
}

#[derive(Deserialize)]
struct LeaseResponse {
    activation_id: String,
    lease: String,
    server_time: i64,
}

#[derive(Deserialize)]
struct ProblemBody {
    code: String,
    #[serde(default)]
    detail: String,
}

impl LicenseClient {
    pub async fn new(config: ClientConfig) -> Result<Self, LicenseError> {
        let mut base = config.server_url.clone();
        let local = matches!(base.host_str(), Some("localhost" | "127.0.0.1" | "[::1]"));
        if !(base.scheme() == "https" || (base.scheme() == "http" && local)) {
            return Err(LicenseError::Config("server URL must use https".into()));
        }
        if config.trust_anchors.is_empty() {
            return Err(LicenseError::Config("at least one trust anchor is required".into()));
        }
        if !base.path().ends_with('/') {
            base.set_path(&format!("{}/", base.path()));
        }
        let http = reqwest::Client::builder()
            .use_rustls_tls()
            .connect_timeout(Duration::from_secs(10))
            .timeout(Duration::from_secs(30))
            .redirect(reqwest::redirect::Policy::none())
            .user_agent(concat!("licensing-client/", env!("CARGO_PKG_VERSION")))
            .build()
            .map_err(|e| LicenseError::Config(e.to_string()))?;
        let (tx, _) = watch::channel(LicenseState::ActivationRequired);
        Ok(Self { inner: Arc::new(Inner { cfg: config, base, http, lock: Mutex::new(()), tx }) })
    }

    /// Loads and verifies persisted state offline. Never touches the network.
    pub async fn initialise(&self) -> Result<LicenseState, LicenseError> {
        let _g = self.inner.lock.lock().await;
        let mut p = self.load()?;
        let s = self.evaluate(&mut p);
        self.save(&p)?;
        Ok(self.publish(s))
    }

    pub fn status(&self) -> LicenseState {
        self.inner.tx.borrow().clone()
    }

    pub fn has_feature(&self, feature: &str) -> bool {
        match &*self.inner.tx.borrow() {
            LicenseState::Active { features, .. } | LicenseState::Trial { features, .. } => features.iter().any(|f| f == feature),
            _ => false,
        }
    }

    pub fn subscribe(&self) -> LicenseStateReceiver {
        self.inner.tx.subscribe()
    }

    /// Starts a customer-approved activation, optionally pre-selecting a licence code.
    pub async fn begin_activation(&self, licence_code: Option<&str>) -> Result<ActivationSession, LicenseError> {
        self.begin("api/v1/activation-intents", "activation", licence_code).await
    }

    /// Starts a trial request; complete it with [`finish_activation`](Self::finish_activation).
    pub async fn request_trial(&self) -> Result<ActivationSession, LicenseError> {
        self.begin("api/v1/trials/intents", "trial", None).await
    }

    async fn begin(&self, path: &str, mode: &str, licence_code: Option<&str>) -> Result<ActivationSession, LicenseError> {
        let kp = &self.inner.cfg.key_provider;
        let key = kp.create_key("installation")?;
        let result = async {
            let public_key = kp.public_key(&key)?;
            let signals = tokio::task::spawn_blocking(device::collect_signals).await.map_err(|e| LicenseError::Protocol(e.to_string()))?;
            let body = serde_json::json!({
                "product_id": self.inner.cfg.product_id,
                "mode": mode,
                "licence_code": licence_code,
                "installation_public_key": B64.encode(public_key),
                "key_algorithm": "ES256",
                "key_storage_class": kp.storage_class(),
                "hardware_signals": signals,
                "display_name": self.inner.cfg.display_name.clone().unwrap_or_else(device::default_display_name),
            });
            let r: IntentResponse = json(&self.send(self.post(path)?.body(body.to_string())).await?)?;
            Ok::<_, LicenseError>(ActivationSession {
                intent_id: r.intent_id,
                user_code: r.user_code,
                verification_uri_complete: if r.verification_uri_complete.is_empty() { r.verification_uri.clone() } else { r.verification_uri_complete },
                verification_uri: r.verification_uri,
                expires_at: self.inner.cfg.clock.now() + Duration::from_secs(r.expires_in.min(3600)),
                poll_interval: Duration::from_secs(r.poll_interval.clamp(1, 60)),
                secret: r.intent_secret,
                key: key.clone(),
            })
        }
        .await;
        match result {
            Ok(s) => {
                if !self.status().is_licensed() {
                    self.publish(LicenseState::ActivationPending);
                }
                Ok(s)
            }
            Err(e) => {
                let _ = kp.delete_key(&key);
                Err(e)
            }
        }
    }

    /// Waits for the customer's approval, proves key possession and stores the
    /// verified lease. Device-limit and licence problems are returned as states;
    /// the session stays valid for a retry until it expires.
    pub async fn finish_activation(&self, session: ActivationSession) -> Result<LicenseState, LicenseError> {
        let status_path = format!("api/v1/activation-intents/{}", session.intent_id);
        loop {
            if self.inner.cfg.clock.now() >= session.expires_at {
                return self.fail_activation(problem("INTENT_EXPIRED")).await;
            }
            let req = self.inner.http.get(self.url(&status_path)?).bearer_auth(&session.secret);
            match self.send(req).await.and_then(|b| json::<StatusResponse>(&b)) {
                Ok(s) if s.status == "approved" || s.status == "completed" => break,
                Ok(s) if s.status == "pending" => {}
                Ok(s) if s.status == "denied" => return self.fail_activation(problem("ACTIVATION_DENIED")).await,
                Ok(s) if s.status == "expired" => return self.fail_activation(problem("INTENT_EXPIRED")).await,
                Ok(s) => return self.fail_activation(LicenseError::Protocol(format!("unknown intent status {:?}", s.status))).await,
                Err(e) if e.is_transient() => {}
                Err(e) => return self.fail_activation(e).await,
            }
            tokio::time::sleep(session.poll_interval).await;
        }

        let _g = self.inner.lock.lock().await;
        let mut p = self.load()?;
        let res = self
            .post_with_proof(&session.intent_id, &session.key, &format!("{status_path}/challenges"), &format!("{status_path}/complete"), Some(&session.secret))
            .await;
        match res {
            Ok(body) => {
                let r: LeaseResponse = json(&body)?;
                let old_key = p.key.clone();
                let s = self.accept_lease(&mut p, r, &session.key).await?;
                if let Some(old) = old_key.filter(|k| *k != session.key.0) {
                    let _ = self.inner.cfg.key_provider.delete_key(&KeyHandle(old));
                }
                Ok(s)
            }
            Err(LicenseError::Problem { code, .. }) if code == "DEVICE_LIMIT_REACHED" => Ok(self.publish(LicenseState::DeviceLimitReached)),
            Err(LicenseError::Problem { code, .. }) if authoritative(&code, false).is_some() => {
                Ok(self.publish(authoritative(&code, false).unwrap_or(Terminal::Revoked).state()))
            }
            Err(e) => {
                let s = self.evaluate(&mut p);
                self.publish(s);
                Err(e)
            }
        }
    }

    async fn fail_activation(&self, e: LicenseError) -> Result<LicenseState, LicenseError> {
        let _g = self.inner.lock.lock().await;
        let mut p = self.load()?;
        let s = self.evaluate(&mut p);
        self.publish(s);
        Err(e)
    }

    /// Renews the lease online. Transport failures keep a valid cached lease and
    /// return the error so the host can show why it is offline.
    pub async fn refresh(&self) -> Result<LicenseState, LicenseError> {
        let _g = self.inner.lock.lock().await;
        let mut p = self.load()?;
        let (Some(id), Some(key)) = (p.activation_id.clone(), p.key.clone().map(KeyHandle)) else {
            let s = self.evaluate(&mut p);
            self.save(&p)?;
            return Ok(self.publish(s));
        };
        let base = format!("api/v1/activations/{id}");
        match self.post_with_proof(&id, &key, &format!("{base}/challenges"), &format!("{base}/renew"), None).await {
            Ok(body) => {
                let r: LeaseResponse = json(&body)?;
                self.accept_lease(&mut p, r, &key).await
            }
            Err(LicenseError::Problem { code, status, detail }) => {
                let trial = self.cached_claims(&p).map(|c| c.trial).unwrap_or(false);
                match authoritative(&code, trial) {
                    Some(t) => {
                        p.lease = None;
                        p.terminal = Some(t);
                        self.save(&p)?;
                        Ok(self.publish(t.state()))
                    }
                    None => {
                        let s = self.evaluate(&mut p);
                        self.publish(s);
                        Err(LicenseError::Problem { code, status, detail })
                    }
                }
            }
            Err(e) => {
                let s = self.evaluate(&mut p);
                self.save(&p)?;
                let offline = e.is_transient() && matches!(s, LicenseState::OfflineLeaseExpired | LicenseState::OnlineVerificationRequired);
                self.publish(if offline { LicenseState::ServiceUnavailable } else { s });
                Err(e)
            }
        }
    }

    /// Deauthorises this installation on the server and forgets it locally.
    pub async fn deactivate(&self) -> Result<(), LicenseError> {
        let _g = self.inner.lock.lock().await;
        let mut p = self.load()?;
        let (Some(id), Some(key)) = (p.activation_id.clone(), p.key.clone().map(KeyHandle)) else {
            return Ok(());
        };
        let base = format!("api/v1/activations/{id}");
        match self.post_with_proof(&id, &key, &format!("{base}/challenges"), &format!("{base}/deactivate"), None).await {
            Ok(_) => {}
            Err(LicenseError::Problem { code, .. }) if code == "DEVICE_DEAUTHORISED" => {}
            Err(e) => return Err(e),
        }
        let _ = self.inner.cfg.key_provider.delete_key(&key);
        p = Persisted { manifest: p.manifest.take(), high_water: p.high_water, ..Default::default() };
        self.save(&p)?;
        self.publish(LicenseState::ActivationRequired);
        Ok(())
    }

    /// Renews on start and then roughly every 24 hours (± 1 hour jitter),
    /// backing off exponentially on transient failures.
    pub fn spawn_renewal(&self) -> tokio::task::JoinHandle<()> {
        let c = self.clone();
        tokio::spawn(async move {
            let mut backoff = 60;
            loop {
                let wait = match c.refresh().await {
                    Err(e) if e.is_transient() => {
                        let w = backoff;
                        backoff = (backoff * 2).min(MAX_BACKOFF_SECS);
                        w + OsRng.next_u64() % (w / 2 + 1)
                    }
                    _ => {
                        backoff = 60;
                        RENEW_EVERY_SECS + OsRng.next_u64() % 7200
                    }
                };
                tokio::time::sleep(Duration::from_secs(wait)).await;
            }
        })
    }

    // --- state ---

    fn evaluate(&self, p: &mut Persisted) -> LicenseState {
        let now = unix(self.inner.cfg.clock.now());
        if now < p.high_water - CLOCK_TOLERANCE_SECS {
            return LicenseState::ClockIntegrityFailure;
        }
        p.high_water = p.high_water.max(now);
        if p.lease.is_none() {
            return p.terminal.map_or(LicenseState::ActivationRequired, Terminal::state);
        }
        let c = match self.cached_claims(p) {
            Ok(c) => c,
            Err(LicenseError::Device(DeviceError::KeyNotFound(_) | DeviceError::CorruptKey)) => return LicenseState::ActivationRequired,
            Err(_) => return LicenseState::OnlineVerificationRequired,
        };
        match crypto::check_lease_time(&c, now) {
            Ok(()) if c.trial => LicenseState::Trial { expires_at: from_unix(c.licence_valid_until), features: c.features },
            Ok(()) => LicenseState::Active { valid_until: from_unix(c.licence_valid_until), offline_until: from_unix(c.expires_at), features: c.features },
            Err(CertError::LicenceExpired) if c.trial => LicenseState::TrialExpired,
            Err(CertError::LicenceExpired) => LicenseState::Expired,
            Err(CertError::LeaseExpired) => LicenseState::OfflineLeaseExpired,
            Err(_) if now < c.licence_valid_from => LicenseState::Scheduled { valid_from: from_unix(c.licence_valid_from) },
            Err(_) => LicenseState::OnlineVerificationRequired,
        }
    }

    /// Fully verifies the cached lease except for its time window.
    fn cached_claims(&self, p: &Persisted) -> Result<LeaseClaims, LicenseError> {
        let (Some(lease), Some(manifest), Some(id), Some(key)) = (&p.lease, &p.manifest, &p.activation_id, &p.key) else {
            return Err(LicenseError::Protocol("no cached lease".into()));
        };
        let lease = decode(lease)?;
        let manifest = crypto::verify_manifest(&decode(manifest)?, &self.inner.cfg.trust_anchors)?;
        let public_key = self.inner.cfg.key_provider.public_key(&KeyHandle(key.clone()))?;
        let expect = LeaseExpectations { product_id: &self.inner.cfg.product_id, installation_id: id, device_public_key: &public_key };
        Ok(crypto::verify_lease_untimed(&lease, &manifest, &expect)?)
    }

    /// Verifies a lease from the server and makes it the current state.
    async fn accept_lease(&self, p: &mut Persisted, r: LeaseResponse, key: &KeyHandle) -> Result<LicenseState, LicenseError> {
        let lease = decode(&r.lease)?;
        let kid = crypto::certificate_kid(&lease)?;
        let cached = p.manifest.as_deref().map(decode).transpose()?.and_then(|m| crypto::verify_manifest(&m, &self.inner.cfg.trust_anchors).ok());
        if !cached.as_ref().is_some_and(|m| m.keys.iter().any(|k| k.kid == kid)) {
            let raw = self.send(self.inner.http.get(self.url("api/v1/trust/manifest")?)).await?;
            let fresh: Manifest = crypto::verify_manifest(&raw, &self.inner.cfg.trust_anchors)?;
            if cached.is_some_and(|old| fresh.issued_at < old.issued_at) {
                return Err(CertError::InvalidClaims("trust manifest rollback".into()).into());
            }
            p.manifest = Some(B64.encode(raw));
        }
        let candidate = Persisted {
            activation_id: Some(r.activation_id),
            lease: Some(r.lease),
            manifest: p.manifest.clone(),
            key: Some(key.0.clone()),
            // A successful online exchange re-baselines the clock.
            high_water: r.server_time,
            terminal: None,
        };
        self.cached_claims(&candidate)?;
        *p = candidate;
        let s = self.evaluate(p);
        self.save(p)?;
        Ok(self.publish(s))
    }

    fn load(&self) -> Result<Persisted, LicenseError> {
        // Corrupt state is discarded; the lease is re-obtained online.
        Ok(self.inner.cfg.storage.load(STATE_KEY)?.and_then(|b| serde_json::from_slice(&b).ok()).unwrap_or_default())
    }

    fn save(&self, p: &Persisted) -> Result<(), LicenseError> {
        let bytes = serde_json::to_vec(p).map_err(|e| LicenseError::Protocol(e.to_string()))?;
        Ok(self.inner.cfg.storage.store_atomic(STATE_KEY, &bytes)?)
    }

    fn publish(&self, s: LicenseState) -> LicenseState {
        self.inner.tx.send_replace(s.clone());
        s
    }

    // --- HTTP ---

    fn url(&self, path: &str) -> Result<Url, LicenseError> {
        self.inner.base.join(path).map_err(|e| LicenseError::Config(e.to_string()))
    }

    fn post(&self, path: &str) -> Result<reqwest::RequestBuilder, LicenseError> {
        Ok(self.inner.http.post(self.url(path)?).header("Content-Type", "application/json"))
    }

    async fn post_with_proof(&self, subject: &str, key: &KeyHandle, challenge_path: &str, path: &str, bearer: Option<&str>) -> Result<Vec<u8>, LicenseError> {
        let with_bearer = |rb: reqwest::RequestBuilder| match bearer {
            Some(b) => rb.bearer_auth(b),
            None => rb,
        };
        let ch: ChallengeResponse = json(&self.send(with_bearer(self.post(challenge_path)?.body("{}"))).await?)?;
        let challenge = decode(&ch.challenge)?;
        let url = self.url(path)?;
        let body: &'static [u8] = b"{}";
        let ts = unix(self.inner.cfg.clock.now());
        let op = uuid_v4();
        let msg = crypto::proof_message(subject, "POST", url.path(), body, &challenge, ts, &op);
        let sig = self.inner.cfg.key_provider.sign(key, &msg)?;
        let req = self
            .inner
            .http
            .post(url)
            .header("Content-Type", "application/json")
            .header("X-Licensing-Challenge", ch.challenge)
            .header("X-Licensing-Timestamp", ts.to_string())
            .header("X-Licensing-Op", op)
            .header("X-Licensing-Signature", B64.encode(sig))
            .body(body);
        self.send(with_bearer(req)).await
    }

    async fn send(&self, req: reqwest::RequestBuilder) -> Result<Vec<u8>, LicenseError> {
        let mut resp = req.send().await.map_err(classify)?;
        let status = resp.status();
        let mut body = Vec::new();
        while let Some(chunk) = resp.chunk().await.map_err(classify)? {
            if body.len() + chunk.len() > MAX_RESPONSE_BYTES {
                return Err(LicenseError::Protocol("response too large".into()));
            }
            body.extend_from_slice(&chunk);
        }
        if status.is_success() {
            return Ok(body);
        }
        match serde_json::from_slice::<ProblemBody>(&body) {
            Ok(p) if !status.is_server_error() => Err(LicenseError::Problem { status: status.as_u16(), code: p.code, detail: p.detail }),
            _ => Err(LicenseError::Server(status.as_u16())),
        }
    }
}

/// Renewal problems that mean the cached lease must no longer be used.
fn authoritative(code: &str, trial: bool) -> Option<Terminal> {
    match code {
        "LICENCE_SUSPENDED" | "LICENCE_REVOKED" => Some(Terminal::Revoked),
        "LICENCE_EXPIRED" if trial => Some(Terminal::TrialExpired),
        "LICENCE_EXPIRED" => Some(Terminal::Expired),
        "DEVICE_DEAUTHORISED" => Some(Terminal::Deauthorised),
        _ => None,
    }
}

fn problem(code: &str) -> LicenseError {
    LicenseError::Problem { status: 0, code: code.into(), detail: String::new() }
}

fn json<T: DeserializeOwned>(b: &[u8]) -> Result<T, LicenseError> {
    serde_json::from_slice(b).map_err(|e| LicenseError::Protocol(format!("malformed response: {e}")))
}

fn decode(s: &str) -> Result<Vec<u8>, LicenseError> {
    B64.decode(s).map_err(|_| LicenseError::Protocol("invalid base64url".into()))
}

/// Maps a reqwest failure onto the distinctions hosts show to users.
fn classify(e: reqwest::Error) -> LicenseError {
    if e.is_timeout() {
        return LicenseError::Timeout;
    }
    let mut chain = e.to_string();
    let mut src = std::error::Error::source(&e);
    while let Some(s) = src {
        chain.push_str(": ");
        chain.push_str(&s.to_string());
        src = s.source();
    }
    // shortcut: matches error text because reqwest exposes no typed DNS/TLS kinds; revisit on reqwest upgrades.
    let lower = chain.to_ascii_lowercase();
    if lower.contains("dns") || lower.contains("lookup") || lower.contains("resolve") {
        LicenseError::Dns(chain)
    } else if lower.contains("certificate") || lower.contains("tls") || lower.contains("handshake") {
        LicenseError::Tls(chain)
    } else {
        LicenseError::Network(chain)
    }
}

fn uuid_v4() -> String {
    let mut b = [0u8; 16];
    OsRng.fill_bytes(&mut b);
    b[6] = (b[6] & 0x0f) | 0x40;
    b[8] = (b[8] & 0x3f) | 0x80;
    let h: String = b.iter().map(|x| format!("{x:02x}")).collect();
    format!("{}-{}-{}-{}-{}", &h[..8], &h[8..12], &h[12..16], &h[16..20], &h[20..])
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::{Clock, LicenseStorage, MemoryStorage, SoftwareKeyProvider, VerifyingKey};
    use std::sync::atomic::{AtomicI64, Ordering};

    struct FakeClock(AtomicI64);
    impl Clock for FakeClock {
        fn now(&self) -> SystemTime {
            from_unix(self.0.load(Ordering::SeqCst))
        }
    }

    fn fixture(name: &str) -> Vec<u8> {
        std::fs::read(format!("{}/../../tests/fixtures/{name}", env!("CARGO_MANIFEST_DIR"))).unwrap()
    }

    /// A client whose storage already holds the Go-issued fixture lease.
    async fn fixture_client() -> (LicenseClient, Arc<FakeClock>, Arc<MemoryStorage>) {
        let v: serde_json::Value = serde_json::from_slice(&fixture("vectors.json")).unwrap();
        let hexv = |k: &str| hex::decode(v[k].as_str().unwrap()).unwrap();
        let root = VerifyingKey::from_bytes(&hexv("root_public_key_hex").try_into().unwrap()).unwrap();
        let keys = Arc::new(MemoryStorage::default());
        keys.store_atomic("key-fixture", &hexv("device_private_key_hex")).unwrap();
        let storage = Arc::new(MemoryStorage::default());
        let p = Persisted {
            activation_id: Some(v["installation_id"].as_str().unwrap().into()),
            lease: Some(B64.encode(fixture("lease.cose"))),
            manifest: Some(B64.encode(fixture("manifest.cose"))),
            key: Some("fixture".into()),
            high_water: 0,
            terminal: None,
        };
        storage.store_atomic(STATE_KEY, &serde_json::to_vec(&p).unwrap()).unwrap();
        let clock = Arc::new(FakeClock(AtomicI64::new(v["eval_time"].as_i64().unwrap())));
        let cfg = ClientConfig {
            product_id: v["product_id"].as_str().unwrap().into(),
            server_url: "https://licensing.invalid/".parse().unwrap(),
            trust_anchors: vec![root],
            storage: storage.clone(),
            key_provider: Arc::new(SoftwareKeyProvider::new(keys)),
            clock: clock.clone(),
            display_name: None,
        };
        (LicenseClient::new(cfg).await.unwrap(), clock, storage)
    }

    #[tokio::test]
    #[ignore = "needs the golden fixtures from the licensing repo (tests/fixtures)"]
    async fn offline_lifecycle_and_clock_rollback() {
        let (c, clock, _) = fixture_client().await;
        let s = c.initialise().await.unwrap();
        assert!(matches!(s, LicenseState::Active { .. }), "{s:?}");
        assert!(c.has_feature("pro") && !c.has_feature("enterprise"));

        clock.0.fetch_add(6 * 86400, Ordering::SeqCst);
        assert!(c.initialise().await.unwrap().is_licensed());

        // Moving the clock back past the tolerance is detected...
        clock.0.fetch_sub(86400, Ordering::SeqCst);
        assert_eq!(c.initialise().await.unwrap(), LicenseState::ClockIntegrityFailure);
        // ...a small NTP-style correction is not.
        clock.0.fetch_add(86400 - 300, Ordering::SeqCst);
        assert!(c.initialise().await.unwrap().is_licensed());

        clock.0.fetch_add(300 + 86400, Ordering::SeqCst); // exactly issued_at + 7 days
        assert_eq!(c.initialise().await.unwrap(), LicenseState::OfflineLeaseExpired);
    }

    #[tokio::test]
    #[ignore = "needs the golden fixtures from the licensing repo (tests/fixtures)"]
    async fn missing_key_or_corrupt_state_requires_activation() {
        let (c, _, storage) = fixture_client().await;
        storage.store_atomic(STATE_KEY, b"not json").unwrap();
        assert_eq!(c.initialise().await.unwrap(), LicenseState::ActivationRequired);
    }
}
