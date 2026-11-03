//! Offline verification of the signed artefacts in protocol v1
//! (`docs/protocol.md`): root-signed trust manifests, issuer-signed activation
//! leases, and the canonical device-proof message.
//!
//! Everything here is pure: no I/O, no clock. Callers pass the evaluation time.

use ciborium::Value;
use ed25519_dalek::{Signature, VerifyingKey};
use serde::{de::DeserializeOwned, Deserialize};
use sha2::{Digest, Sha256};

pub const MAX_CERT_SIZE: usize = 16 * 1024;
pub const MAX_LEASE_LIFETIME_SECS: i64 = 7 * 24 * 3600;
pub const PURPOSE_LEASE: &str = "activation-lease";
pub const PURPOSE_MANIFEST: &str = "trust-manifest";

const COSE_SIGN1_TAG: u64 = 18;
const ALG_EDDSA: i128 = -8;
const MAX_CBOR_DEPTH: usize = 8;

/// Why a certificate was rejected.
#[derive(Debug, Clone, PartialEq, Eq, thiserror::Error)]
pub enum CertError {
    #[error("certificate exceeds {MAX_CERT_SIZE} bytes")]
    TooLarge,
    #[error("malformed COSE_Sign1 envelope")]
    Malformed,
    #[error("unsupported or unexpected protected header")]
    UnsupportedHeader,
    #[error("signing key {0:?} is not trusted")]
    UnknownKey(String),
    #[error("signature verification failed")]
    BadSignature,
    #[error("invalid claims: {0}")]
    InvalidClaims(String),
    #[error("certificate is for another product")]
    WrongProduct,
    #[error("certificate is for another installation")]
    WrongInstallation,
    #[error("certificate is bound to another device key")]
    WrongDeviceKey,
    #[error("lease is not valid yet")]
    NotYetValid,
    #[error("lease has expired")]
    LeaseExpired,
    #[error("licence has expired")]
    LicenceExpired,
}

/// Signed claims of an activation lease.
#[derive(Debug, Clone, PartialEq, Eq, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct LeaseClaims {
    pub schema: u64,
    pub purpose: String,
    pub issuer: String,
    pub lease_id: String,
    pub licence_id: String,
    pub licence_revision: u64,
    pub machine_id: String,
    pub installation_id: String,
    pub product_id: String,
    #[serde(with = "serde_bytes")]
    pub device_key_sha256: Vec<u8>,
    pub features: Vec<String>,
    pub licence_valid_from: i64,
    pub licence_valid_until: i64,
    pub issued_at: i64,
    pub not_before: i64,
    pub expires_at: i64,
    pub trial: bool,
}

/// One issuer key authorised by the root.
#[derive(Debug, Clone, PartialEq, Eq, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ManifestKey {
    pub kid: String,
    pub alg: String,
    #[serde(with = "serde_bytes")]
    pub public_key: Vec<u8>,
    pub purpose: String,
    pub not_before: i64,
    pub not_after: i64,
}

/// Root-signed list of issuer keys.
#[derive(Debug, Clone, PartialEq, Eq, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Manifest {
    pub schema: u64,
    pub purpose: String,
    pub issued_at: i64,
    pub keys: Vec<ManifestKey>,
}

/// What a lease must be bound to on this installation.
#[derive(Debug, Clone, Copy)]
pub struct LeaseExpectations<'a> {
    pub product_id: &'a str,
    pub installation_id: &'a str,
    /// SEC1 uncompressed P-256 public key of this installation.
    pub device_public_key: &'a [u8],
}

struct Sign1 {
    kid: String,
    protected: Vec<u8>,
    payload: Vec<u8>,
    signature: Vec<u8>,
}

fn open_sign1(data: &[u8]) -> Result<Sign1, CertError> {
    if data.len() > MAX_CERT_SIZE {
        return Err(CertError::TooLarge);
    }
    let mut rd = data;
    let v: Value = ciborium::de::from_reader_with_recursion_limit(&mut rd, MAX_CBOR_DEPTH).map_err(|_| CertError::Malformed)?;
    if !rd.is_empty() {
        return Err(CertError::Malformed);
    }
    let Value::Tag(COSE_SIGN1_TAG, inner) = v else {
        return Err(CertError::Malformed);
    };
    let Value::Array(items) = *inner else {
        return Err(CertError::Malformed);
    };
    let Ok::<[Value; 4], _>([Value::Bytes(protected), Value::Map(unprotected), Value::Bytes(payload), Value::Bytes(signature)]) = items.try_into() else {
        return Err(CertError::Malformed);
    };
    if !unprotected.is_empty() {
        return Err(CertError::UnsupportedHeader);
    }
    let mut rd = protected.as_slice();
    let hdr: Value = ciborium::de::from_reader_with_recursion_limit(&mut rd, 2).map_err(|_| CertError::Malformed)?;
    let Value::Map(entries) = hdr else {
        return Err(CertError::UnsupportedHeader);
    };
    if !rd.is_empty() || entries.len() != 2 {
        return Err(CertError::UnsupportedHeader);
    }
    let (mut alg_ok, mut kid) = (false, None);
    for (k, v) in entries {
        match (k.as_integer().map(i128::from), v) {
            (Some(1), Value::Integer(a)) if !alg_ok && i128::from(a) == ALG_EDDSA => alg_ok = true,
            (Some(4), Value::Bytes(b)) if kid.is_none() => kid = Some(String::from_utf8(b).map_err(|_| CertError::UnsupportedHeader)?),
            _ => return Err(CertError::UnsupportedHeader),
        }
    }
    let (true, Some(kid)) = (alg_ok, kid) else {
        return Err(CertError::UnsupportedHeader);
    };
    Ok(Sign1 { kid, protected, payload, signature })
}

impl Sign1 {
    fn verify(&self, key: &VerifyingKey) -> Result<(), CertError> {
        let tbs = Value::Array(vec![
            Value::Text("Signature1".into()),
            Value::Bytes(self.protected.clone()),
            Value::Bytes(Vec::new()),
            Value::Bytes(self.payload.clone()),
        ]);
        let mut buf = Vec::new();
        ciborium::ser::into_writer(&tbs, &mut buf).map_err(|_| CertError::Malformed)?;
        let sig = Signature::from_slice(&self.signature).map_err(|_| CertError::BadSignature)?;
        key.verify_strict(&buf, &sig).map_err(|_| CertError::BadSignature)
    }

    fn claims<T: DeserializeOwned>(&self) -> Result<T, CertError> {
        let mut rd = self.payload.as_slice();
        let out = ciborium::de::from_reader_with_recursion_limit(&mut rd, MAX_CBOR_DEPTH).map_err(|e| CertError::InvalidClaims(e.to_string()))?;
        if !rd.is_empty() {
            return Err(CertError::InvalidClaims("trailing bytes".into()));
        }
        Ok(out)
    }
}

/// Verifies a trust manifest against the embedded root trust anchors.
pub fn verify_manifest(data: &[u8], trust_anchors: &[VerifyingKey]) -> Result<Manifest, CertError> {
    let s = open_sign1(data)?;
    if !trust_anchors.iter().any(|k| s.verify(k).is_ok()) {
        return Err(CertError::BadSignature);
    }
    let m: Manifest = s.claims()?;
    if m.schema != 1 || m.purpose != PURPOSE_MANIFEST {
        return Err(CertError::InvalidClaims("not a v1 trust manifest".into()));
    }
    Ok(m)
}

/// Kid of the key that signed a certificate, without verifying it.
pub fn certificate_kid(data: &[u8]) -> Result<String, CertError> {
    open_sign1(data).map(|s| s.kid)
}

/// Verifies signature, issuer and bindings of a lease, but not its time window.
pub fn verify_lease_untimed(data: &[u8], manifest: &Manifest, expect: &LeaseExpectations) -> Result<LeaseClaims, CertError> {
    let s = open_sign1(data)?;
    let mk = manifest.keys.iter().find(|k| k.kid == s.kid).ok_or_else(|| CertError::UnknownKey(s.kid.clone()))?;
    let key_bytes: [u8; 32] = mk.public_key.as_slice().try_into().map_err(|_| CertError::UnknownKey(s.kid.clone()))?;
    if mk.alg != "EdDSA" || mk.purpose != PURPOSE_LEASE {
        return Err(CertError::UnknownKey(s.kid.clone()));
    }
    let key = VerifyingKey::from_bytes(&key_bytes).map_err(|_| CertError::UnknownKey(s.kid.clone()))?;
    s.verify(&key)?;
    let c: LeaseClaims = s.claims()?;
    if c.schema != 1 || c.purpose != PURPOSE_LEASE {
        return Err(CertError::InvalidClaims("not a v1 activation lease".into()));
    }
    if c.issued_at < mk.not_before || c.issued_at >= mk.not_after {
        return Err(CertError::UnknownKey(s.kid));
    }
    if c.not_before < c.issued_at
        || c.expires_at <= c.not_before
        || c.expires_at - c.issued_at > MAX_LEASE_LIFETIME_SECS
        || c.licence_valid_until <= c.licence_valid_from
    {
        return Err(CertError::InvalidClaims("invalid validity interval".into()));
    }
    if c.product_id != expect.product_id {
        return Err(CertError::WrongProduct);
    }
    if c.installation_id != expect.installation_id {
        return Err(CertError::WrongInstallation);
    }
    if c.device_key_sha256[..] != Sha256::digest(expect.device_public_key)[..] {
        return Err(CertError::WrongDeviceKey);
    }
    Ok(c)
}

/// Checks a verified lease's time window at `now` (Unix seconds).
pub fn check_lease_time(c: &LeaseClaims, now: i64) -> Result<(), CertError> {
    if now >= c.licence_valid_until {
        Err(CertError::LicenceExpired)
    } else if now < c.not_before {
        Err(CertError::NotYetValid)
    } else if now >= c.expires_at {
        Err(CertError::LeaseExpired)
    } else {
        Ok(())
    }
}

/// Full offline verification of a lease at `now` (Unix seconds).
pub fn verify_lease(data: &[u8], manifest: &Manifest, expect: &LeaseExpectations, now: i64) -> Result<LeaseClaims, CertError> {
    let c = verify_lease_untimed(data, manifest, expect)?;
    check_lease_time(&c, now)?;
    Ok(c)
}

/// The canonical bytes an installation signs to prove key possession.
pub fn proof_message(subject: &str, method: &str, path: &str, body: &[u8], challenge: &[u8], timestamp: i64, op: &str) -> Vec<u8> {
    let v = Value::Array(vec![
        Value::Text("licensing-device-proof".into()),
        Value::Integer(1.into()),
        Value::Text(subject.into()),
        Value::Text(method.into()),
        Value::Text(path.into()),
        Value::Bytes(Sha256::digest(body).to_vec()),
        Value::Bytes(challenge.to_vec()),
        Value::Integer(timestamp.into()),
        Value::Text(op.into()),
    ]);
    let mut buf = Vec::new();
    ciborium::ser::into_writer(&v, &mut buf).expect("writing CBOR to a Vec cannot fail");
    buf
}
