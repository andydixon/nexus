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

#[cfg(test)]
mod tests {
    //! Self-contained vectors: a root and an issuer key derived from fixed bytes, a manifest signed
    //! by the root and a lease signed by the issuer, built with the same COSE_Sign1 layout the
    //! server emits (protected header {1: -8, 4: kid}, empty unprotected header, tag 18).
    use super::*;
    use ciborium::Value;
    use ed25519_dalek::{Signer, SigningKey};

    const NOW: i64 = 1_800_000_000;
    const PRODUCT: &str = "807e65d4-7301-4f8b-b1a1-9bd66fdd8399";
    const INSTALL: &str = "inst-1";
    const DEVICE_KEY: &[u8] = b"\x04device-public-key-bytes";

    fn key(seed: u8) -> SigningKey {
        SigningKey::from_bytes(&[seed; 32])
    }

    fn sign1(sk: &SigningKey, kid: &str, payload: Vec<u8>, unprotected: Vec<(Value, Value)>) -> Vec<u8> {
        let mut protected = Vec::new();
        let hdr = Value::Map(vec![(Value::Integer(1.into()), Value::Integer((-8).into())), (Value::Integer(4.into()), Value::Bytes(kid.as_bytes().to_vec()))]);
        ciborium::ser::into_writer(&hdr, &mut protected).unwrap();
        let tbs = Value::Array(vec![Value::Text("Signature1".into()), Value::Bytes(protected.clone()), Value::Bytes(vec![]), Value::Bytes(payload.clone())]);
        let mut tbs_bytes = Vec::new();
        ciborium::ser::into_writer(&tbs, &mut tbs_bytes).unwrap();
        let sig = sk.sign(&tbs_bytes).to_bytes().to_vec();
        let env = Value::Tag(
            COSE_SIGN1_TAG,
            Box::new(Value::Array(vec![Value::Bytes(protected), Value::Map(unprotected), Value::Bytes(payload), Value::Bytes(sig)])),
        );
        let mut out = Vec::new();
        ciborium::ser::into_writer(&env, &mut out).unwrap();
        out
    }

    fn map(entries: Vec<(&str, Value)>) -> Vec<u8> {
        let v = Value::Map(entries.into_iter().map(|(k, v)| (Value::Text(k.into()), v)).collect());
        let mut out = Vec::new();
        ciborium::ser::into_writer(&v, &mut out).unwrap();
        out
    }

    fn manifest_payload(issuer: &SigningKey, not_before: i64, not_after: i64) -> Vec<u8> {
        map(vec![
            ("schema", Value::Integer(1.into())),
            ("purpose", Value::Text(PURPOSE_MANIFEST.into())),
            ("issued_at", Value::Integer((NOW - 86_400).into())),
            (
                "keys",
                Value::Array(vec![Value::Map(vec![
                    (Value::Text("kid".into()), Value::Text("issuer-1".into())),
                    (Value::Text("alg".into()), Value::Text("EdDSA".into())),
                    (Value::Text("public_key".into()), Value::Bytes(issuer.verifying_key().to_bytes().to_vec())),
                    (Value::Text("purpose".into()), Value::Text(PURPOSE_LEASE.into())),
                    (Value::Text("not_before".into()), Value::Integer(not_before.into())),
                    (Value::Text("not_after".into()), Value::Integer(not_after.into())),
                ])]),
            ),
        ])
    }

    struct Lease {
        issued_at: i64,
        expires_at: i64,
        licence_valid_until: i64,
        product: &'static str,
        device_key: &'static [u8],
        trial: bool,
    }

    impl Default for Lease {
        fn default() -> Self {
            Self {
                issued_at: NOW - 3600,
                expires_at: NOW - 3600 + 6 * 86_400,
                licence_valid_until: NOW + 365 * 86_400,
                product: PRODUCT,
                device_key: DEVICE_KEY,
                trial: false,
            }
        }
    }

    fn lease_payload(l: &Lease) -> Vec<u8> {
        map(vec![
            ("schema", Value::Integer(1.into())),
            ("purpose", Value::Text(PURPOSE_LEASE.into())),
            ("issuer", Value::Text("issuer-1".into())),
            ("lease_id", Value::Text("lease-1".into())),
            ("licence_id", Value::Text("lic-1".into())),
            ("licence_revision", Value::Integer(3.into())),
            ("machine_id", Value::Text("m-1".into())),
            ("installation_id", Value::Text(INSTALL.into())),
            ("product_id", Value::Text(l.product.into())),
            ("device_key_sha256", Value::Bytes(Sha256::digest(l.device_key).to_vec())),
            ("features", Value::Array(vec![Value::Text("pro".into())])),
            ("licence_valid_from", Value::Integer((NOW - 30 * 86_400).into())),
            ("licence_valid_until", Value::Integer(l.licence_valid_until.into())),
            ("issued_at", Value::Integer(l.issued_at.into())),
            ("not_before", Value::Integer(l.issued_at.into())),
            ("expires_at", Value::Integer(l.expires_at.into())),
            ("trial", Value::Bool(l.trial)),
        ])
    }

    /// (root verifying key, verified manifest, issuer key)
    fn setup() -> (VerifyingKey, Manifest, SigningKey) {
        let (root, issuer) = (key(1), key(2));
        let m = sign1(&root, "root-1", manifest_payload(&issuer, NOW - 365 * 86_400, NOW + 365 * 86_400), vec![]);
        let manifest = verify_manifest(&m, &[root.verifying_key()]).unwrap();
        (root.verifying_key(), manifest, issuer)
    }

    fn expect() -> LeaseExpectations<'static> {
        LeaseExpectations { product_id: PRODUCT, installation_id: INSTALL, device_public_key: DEVICE_KEY }
    }

    #[test]
    fn valid_lease_verifies_and_reports_claims() {
        let (_, manifest, issuer) = setup();
        let lease = sign1(&issuer, "issuer-1", lease_payload(&Lease::default()), vec![]);
        let c = verify_lease(&lease, &manifest, &expect(), NOW).unwrap();
        assert_eq!((c.features.as_slice(), c.trial, c.licence_revision), (&["pro".to_string()][..], false, 3));
        assert_eq!(certificate_kid(&lease).unwrap(), "issuer-1");
    }

    #[test]
    fn manifest_needs_a_trusted_root() {
        let (root, _, issuer) = setup();
        let forged = sign1(&key(9), "root-1", manifest_payload(&issuer, 0, i64::MAX), vec![]);
        assert_eq!(verify_manifest(&forged, &[root]).unwrap_err(), CertError::BadSignature);
        let wrong_purpose = sign1(
            &key(1),
            "root-1",
            map(vec![
                ("schema", Value::Integer(1.into())),
                ("purpose", Value::Text("x".into())),
                ("issued_at", Value::Integer(1.into())),
                ("keys", Value::Array(vec![])),
            ]),
            vec![],
        );
        assert!(matches!(verify_manifest(&wrong_purpose, &[root]), Err(CertError::InvalidClaims(_))));
    }

    #[test]
    fn tampered_payload_and_signature_are_rejected() {
        let (_, manifest, issuer) = setup();
        let good = sign1(&issuer, "issuer-1", lease_payload(&Lease::default()), vec![]);
        // Flip one byte inside the payload (the features string) and one inside the signature.
        let pos = good.windows(3).position(|w| w == b"pro").unwrap();
        for (at, err) in [(pos, CertError::BadSignature), (good.len() - 1, CertError::BadSignature)] {
            let mut t = good.clone();
            t[at] ^= 1;
            assert_eq!(verify_lease(&t, &manifest, &expect(), NOW).unwrap_err(), err);
        }
        assert_eq!(verify_lease(&good[..good.len() - 1], &manifest, &expect(), NOW).unwrap_err(), CertError::Malformed);
        let mut trailing = good.clone();
        trailing.push(0);
        assert_eq!(verify_lease(&trailing, &manifest, &expect(), NOW).unwrap_err(), CertError::Malformed);
        assert_eq!(verify_lease(&vec![0; MAX_CERT_SIZE + 1], &manifest, &expect(), NOW).unwrap_err(), CertError::TooLarge);
    }

    #[test]
    fn lease_must_come_from_a_manifest_key_with_the_right_purpose_and_window() {
        let (root, manifest, issuer) = setup();
        let lease = |sk: &SigningKey, kid: &str, l: &Lease| sign1(sk, kid, lease_payload(l), vec![]);
        // Unknown kid, and the root key itself signing a lease.
        assert!(
            matches!(verify_lease(&lease(&issuer, "issuer-2", &Lease::default()), &manifest, &expect(), NOW), Err(CertError::UnknownKey(k)) if k == "issuer-2")
        );
        assert_eq!(verify_lease(&lease(&key(1), "issuer-1", &Lease::default()), &manifest, &expect(), NOW).unwrap_err(), CertError::BadSignature);
        // Issued outside the issuer key's validity window.
        let narrow = sign1(&key(1), "root-1", manifest_payload(&issuer, NOW - 60, NOW + 60), vec![]);
        let narrow = verify_manifest(&narrow, &[root]).unwrap();
        assert!(matches!(verify_lease(&lease(&issuer, "issuer-1", &Lease::default()), &narrow, &expect(), NOW), Err(CertError::UnknownKey(_))));
        // A manifest key of another purpose or algorithm never signs leases.
        for (field, value) in [("purpose", "trust-manifest"), ("alg", "ES256")] {
            let mut m = manifest.clone();
            match field {
                "purpose" => m.keys[0].purpose = value.into(),
                _ => m.keys[0].alg = value.into(),
            }
            assert!(matches!(verify_lease(&lease(&issuer, "issuer-1", &Lease::default()), &m, &expect(), NOW), Err(CertError::UnknownKey(_))));
        }
    }

    #[test]
    fn bindings_and_validity_are_checked() {
        let (_, manifest, issuer) = setup();
        let lease = |l: &Lease| sign1(&issuer, "issuer-1", lease_payload(l), vec![]);
        assert_eq!(verify_lease(&lease(&Lease { product: "other", ..Default::default() }), &manifest, &expect(), NOW).unwrap_err(), CertError::WrongProduct);
        assert_eq!(
            verify_lease(&lease(&Lease { device_key: b"\x04other", ..Default::default() }), &manifest, &expect(), NOW).unwrap_err(),
            CertError::WrongDeviceKey
        );
        let other_install = LeaseExpectations { installation_id: "inst-2", ..expect() };
        assert_eq!(verify_lease(&lease(&Lease::default()), &manifest, &other_install, NOW).unwrap_err(), CertError::WrongInstallation);
        // Lifetime over seven days, and an inverted window.
        let long = Lease { expires_at: NOW - 3600 + 8 * 86_400, ..Default::default() };
        assert!(matches!(verify_lease(&lease(&long), &manifest, &expect(), NOW), Err(CertError::InvalidClaims(_))));
        let inverted = Lease { expires_at: NOW - 7200, ..Default::default() };
        assert!(matches!(verify_lease(&lease(&inverted), &manifest, &expect(), NOW), Err(CertError::InvalidClaims(_))));
    }

    #[test]
    fn time_window_and_licence_end() {
        let (_, manifest, issuer) = setup();
        let l = Lease::default();
        let lease = sign1(&issuer, "issuer-1", lease_payload(&l), vec![]);
        let c = verify_lease_untimed(&lease, &manifest, &expect()).unwrap();
        assert_eq!(check_lease_time(&c, l.issued_at - 1).unwrap_err(), CertError::NotYetValid);
        assert_eq!(check_lease_time(&c, l.expires_at).unwrap_err(), CertError::LeaseExpired);
        assert_eq!(check_lease_time(&c, l.licence_valid_until).unwrap_err(), CertError::LicenceExpired);
        assert!(check_lease_time(&c, l.expires_at - 1).is_ok());
        // A trial lease whose licence has ended is reported as the licence ending, not the lease.
        let trial = Lease { trial: true, licence_valid_until: NOW - 1, ..Default::default() };
        let t = verify_lease_untimed(&sign1(&issuer, "issuer-1", lease_payload(&trial), vec![]), &manifest, &expect()).unwrap();
        assert!(t.trial);
        assert_eq!(check_lease_time(&t, NOW).unwrap_err(), CertError::LicenceExpired);
    }

    #[test]
    fn envelope_variants_are_refused() {
        let (_, manifest, issuer) = setup();
        let payload = lease_payload(&Lease::default());
        // Anything in the unprotected header (where an attacker could add a kid) is refused.
        let unprot = sign1(&issuer, "issuer-1", payload.clone(), vec![(Value::Integer(4.into()), Value::Bytes(b"issuer-1".to_vec()))]);
        assert_eq!(verify_lease(&unprot, &manifest, &expect(), NOW).unwrap_err(), CertError::UnsupportedHeader);
        // Unknown claim fields are refused (deny_unknown_fields), so a server cannot smuggle extras past old clients.
        let mut extra = Value::Map(vec![]);
        if let Value::Map(m) = &mut extra {
            let base: Value = ciborium::de::from_reader(payload.as_slice()).unwrap();
            if let Value::Map(b) = base {
                m.extend(b);
            }
            m.push((Value::Text("extra".into()), Value::Bool(true)));
        }
        let mut buf = Vec::new();
        ciborium::ser::into_writer(&extra, &mut buf).unwrap();
        assert!(matches!(verify_lease(&sign1(&issuer, "issuer-1", buf, vec![]), &manifest, &expect(), NOW), Err(CertError::InvalidClaims(_))));
        // Not a COSE_Sign1 tag, and a deeply nested payload.
        assert_eq!(verify_lease(&[0x80], &manifest, &expect(), NOW).unwrap_err(), CertError::Malformed);
        let mut deep = vec![0x81u8; 40];
        deep.push(0);
        assert!(verify_lease(&sign1(&issuer, "issuer-1", deep, vec![]), &manifest, &expect(), NOW).unwrap_err().to_string().contains("invalid claims"));
    }

    #[test]
    fn proof_message_is_canonical() {
        let a = proof_message("s", "POST", "/p", b"{}", b"c", 7, "op");
        let b = proof_message("s", "POST", "/p", b"{}", b"c", 7, "op");
        assert_eq!(a, b);
        assert_ne!(a, proof_message("s", "POST", "/p", b"{ }", b"c", 7, "op"));
        assert_ne!(a, proof_message("s", "POST", "/p", b"{}", b"c", 8, "op"));
    }
}
