//! RDP client on IronRDP: TLS with NLA (CredSSP), the server certificate pinned through the
//! caller's host-key check, any transport the caller dials (direct, SOCKS5/HTTP proxy or an SSH
//! tunnel), and the desktop decoded into the same RGBA framebuffer the VNC client uses. Like the
//! other clients, `connect` blocks and the session runs on its own thread.

use super::vnc::{ClipboardFn, FrameFn, Framebuffer};
use super::{CloseFn, ErrorFn};
use crate::Result;
use ironrdp::cliprdr::CliprdrClient;
use ironrdp::cliprdr::backend::{ClipboardMessage, CliprdrBackend};
use ironrdp::cliprdr::pdu::{
    ClipboardFormat, ClipboardFormatId, ClipboardGeneralCapabilityFlags, FileContentsRequest, FileContentsResponse, FormatDataRequest, FormatDataResponse,
    LockDataId,
};
use ironrdp::connector::connection_activation::ConnectionActivationState;
use ironrdp::connector::{self, ClientConnector, ConnectionResult, Credentials, Sequence, ServerName};
use ironrdp::displaycontrol::client::DisplayControlClient;
use ironrdp::dvc::DrdynvcClient;
use ironrdp::graphics::image_processing::PixelFormat;
use ironrdp::input::Database;
pub use ironrdp::input::{MouseButton, MousePosition, Operation, Scancode, WheelRotations};
use ironrdp::pdu::gcc::KeyboardType;
use ironrdp::pdu::rdp::capability_sets::MajorPlatformType;
use ironrdp::pdu::rdp::client_info::{PerformanceFlags, TimezoneInfo};
use ironrdp::session::image::DecodedImage;
use ironrdp::session::{ActiveStageBuilder, ActiveStageOutput, fast_path};
use ironrdp_blocking::Framed;
use parking_lot::Mutex;
use sha2::{Digest, Sha256};
use std::net::TcpStream;
use std::sync::Arc;
use std::sync::mpsc::{self, Receiver, Sender};
use std::time::Duration;

/// Opens the TCP stream to the RDP server (directly, via a proxy or through an SSH tunnel).
pub type Dial = Arc<dyn Fn() -> Result<TcpStream> + Send + Sync>;
/// Decides whether to trust the server certificate, given its `SHA256:<base64>` fingerprint.
pub type CertCheck = Arc<dyn Fn(&str) -> std::result::Result<(), String> + Send + Sync>;

#[derive(Clone)]
pub struct Config {
    /// Used for the TLS server name and CredSSP's target name.
    pub host: String,
    pub username: String,
    pub domain: String,
    pub password: zeroize::Zeroizing<String>,
    pub width: u16,
    pub height: u16,
    /// Also allow plain TLS (graphical logon) for servers without NLA, such as xrdp.
    pub allow_tls_only: bool,
    /// Play the remote desktop's sound locally.
    pub audio: bool,
    /// A local folder to share as a drive (Unix only), and the name it appears under.
    pub shared_folder: Option<(std::path::PathBuf, String)>,
    /// Offer a printer; jobs are saved as PostScript in this folder.
    pub printer: Option<(std::path::PathBuf, super::rdp_devices::PrintedFn)>,
    pub dial: Dial,
    pub check_cert: CertCheck,
    pub connect_timeout: Duration,
    pub on_frame: FrameFn,
    /// Text the remote desktop copied (the app applies its remote-clipboard preference).
    pub on_clipboard: ClipboardFn,
    pub on_error: ErrorFn,
    pub on_close: CloseFn,
}

enum Input {
    Ops(Vec<Operation>),
    Resize(u16, u16),
    /// Offer local clipboard text to the remote desktop.
    Clipboard(String),
    Close,
}

pub struct Client {
    cfg: Config,
    fb: Arc<Mutex<Framebuffer>>,
    tx: Mutex<Option<Sender<Input>>>,
    /// Local text offered to the remote clipboard, read by the clipboard channel on request.
    clip_text: Arc<Mutex<Option<String>>>,
    clip_rx: Mutex<Option<Receiver<ClipboardMessage>>>,
}

type Tls = rustls::StreamOwned<rustls::ClientConnection, TcpStream>;

/// How often the session thread looks for queued input while waiting for server data.
const POLL: Duration = Duration::from_millis(15);

impl Client {
    pub fn new(cfg: Config) -> Arc<Client> {
        Arc::new(Client { cfg, fb: Arc::default(), tx: Mutex::new(None), clip_text: Arc::default(), clip_rx: Mutex::new(None) })
    }

    pub fn framebuffer(&self) -> Arc<Mutex<Framebuffer>> {
        self.fb.clone()
    }

    /// Connects and logs on, then runs the session on a new thread.
    pub fn connect(self: &Arc<Self>) -> Result<()> {
        let (framed, result) = self.handshake()?;
        let (tx, rx) = mpsc::channel();
        *self.tx.lock() = Some(tx);
        let me = self.clone();
        let (on_error, on_close) = (self.cfg.on_error.clone(), self.cfg.on_close.clone());
        std::thread::Builder::new().name("nexus-rdp".into()).spawn(super::guarded(on_error, on_close, move || {
            let r = me.run(framed, result, rx);
            let closing = me.tx.lock().take().is_none();
            if let Err(e) = r
                && !closing
            {
                (me.cfg.on_error)(e.to_string());
            }
            (me.cfg.on_close)();
        }))?;
        Ok(())
    }

    /// Mouse and keyboard input, applied in order. `Database` turns repeats into no-ops.
    pub fn input(&self, ops: Vec<Operation>) {
        self.send(Input::Ops(ops));
    }

    /// Asks the server to resize the desktop (Display Control channel); ignored when it can't.
    pub fn resize(&self, width: u16, height: u16) {
        self.send(Input::Resize(width, height));
    }

    /// Makes `text` the remote clipboard's contents (the remote pastes it on demand).
    pub fn set_clipboard(&self, text: String) {
        self.send(Input::Clipboard(text));
    }

    pub fn close(&self) {
        if let Some(tx) = self.tx.lock().take() {
            let _ = tx.send(Input::Close);
        }
    }

    fn send(&self, i: Input) {
        if let Some(tx) = &*self.tx.lock() {
            let _ = tx.send(i);
        }
    }

    fn connector_config(&self) -> connector::Config {
        let c = &self.cfg;
        connector::Config {
            credentials: Credentials::UsernamePassword { username: c.username.clone(), password: c.password.to_string() },
            domain: (!c.domain.is_empty()).then(|| c.domain.clone()),
            enable_tls: c.allow_tls_only,
            enable_credssp: true,
            keyboard_type: KeyboardType::IbmEnhanced,
            keyboard_subtype: 0,
            keyboard_layout: 0,
            keyboard_functional_keys_count: 12,
            ime_file_name: String::new(),
            dig_product_id: String::new(),
            desktop_size: connector::DesktopSize { width: c.width.clamp(200, 8192), height: c.height.clamp(200, 8192) },
            bitmap: None,
            client_build: 0,
            client_name: "Nexus Terminal".into(),
            client_dir: "C:\\Windows\\System32\\mstscax.dll".into(),
            platform: if cfg!(windows) {
                MajorPlatformType::WINDOWS
            } else if cfg!(target_os = "macos") {
                MajorPlatformType::MACINTOSH
            } else {
                MajorPlatformType::UNIX
            },
            enable_server_pointer: false,
            request_data: None,
            // Also hands the credentials to TLS-only servers (xrdp), which would otherwise show their own login screen.
            autologon: !c.password.is_empty(),
            enable_audio_playback: c.audio,
            // ponytail: no bulk compression (the bitmap codecs already compress); enabling it means
            // carrying the decompressor across reactivations.
            compression_type: None,
            pointer_software_rendering: true,
            multitransport_flags: None,
            performance_flags: PerformanceFlags::default(),
            desktop_scale_factor: 0,
            hardware_id: None,
            license_cache: None,
            timezone_info: TimezoneInfo::default(),
            alternate_shell: String::new(),
            work_dir: String::new(),
        }
    }

    fn handshake(&self) -> Result<(Framed<Tls>, ConnectionResult)> {
        let tcp = (self.cfg.dial)()?;
        tcp.set_read_timeout(Some(self.cfg.connect_timeout.max(Duration::from_secs(5))))?;
        tcp.set_nodelay(true)?;
        let local = tcp.local_addr()?;
        let mut framed = Framed::new(tcp);
        let drdynvc = DrdynvcClient::new().with_dynamic_channel(DisplayControlClient::new(|_| Ok(Vec::new())));
        let mut connector = ClientConnector::new(self.connector_config(), local).with_static_channel(drdynvc);
        if self.cfg.audio {
            connector.attach_static_channel(ironrdp::rdpsnd::client::Rdpsnd::new(Box::new(ironrdp_rdpsnd_native::cpal::RdpsndBackend::new())));
        }
        if self.cfg.shared_folder.is_some() || self.cfg.printer.is_some() {
            let mut devices = super::rdp_devices::Devices::default();
            let mut drives = vec![];
            #[cfg(unix)]
            if let Some((folder, name)) = &self.cfg.shared_folder {
                devices = devices.with_drive(folder).map_err(|e| format!("shared folder {}: {e}", folder.display()))?;
                drives.push((1, name.clone()));
            }
            if let Some((dir, on_printed)) = &self.cfg.printer {
                devices = devices.with_printer(dir.clone(), on_printed.clone());
            }
            let mut rdpdr = ironrdp::rdpdr::Rdpdr::new(Box::new(devices), "Nexus".into());
            if !drives.is_empty() {
                rdpdr = rdpdr.with_drives(Some(drives));
            }
            if self.cfg.printer.is_some() {
                rdpdr = rdpdr.with_printer(2, "Nexus Terminal".into());
            }
            connector.attach_static_channel(rdpdr);
        }
        let (clip_tx, clip_rx) = mpsc::channel();
        connector.attach_static_channel(CliprdrClient::new(Box::new(TextClipboard {
            tx: clip_tx,
            text: self.clip_text.clone(),
            on_text: self.cfg.on_clipboard.clone(),
        })));
        *self.clip_rx.lock() = Some(clip_rx);
        let should_upgrade = ironrdp_blocking::connect_begin(&mut framed, &mut connector).map_err(|e| format!("RDP connection failed: {e}"))?;

        let tcp = framed.into_inner_no_leftover();
        let (tls, cert) = tls_upgrade(tcp, &self.cfg.host)?;
        // Pin before CredSSP: no credentials leave the machine for an untrusted certificate.
        (self.cfg.check_cert)(&fingerprint(&cert))?;
        let upgraded = ironrdp_blocking::mark_as_upgraded(should_upgrade, &mut connector);
        let mut framed = Framed::new(tls);
        let result = ironrdp_blocking::connect_finalize(
            upgraded,
            connector,
            &mut framed,
            &mut NoNetwork,
            ServerName::new(self.cfg.host.clone()),
            server_public_key(&cert)?,
            None,
        )
        .map_err(|e| format!("RDP logon failed: {}", describe(&e)))?;
        Ok((framed, result))
    }

    fn run(&self, mut framed: Framed<Tls>, result: ConnectionResult, rx: Receiver<Input>) -> Result<()> {
        framed.get_inner_mut().0.sock.set_read_timeout(Some(POLL))?;
        let size = result.desktop_size;
        let mut image = DecodedImage::new(PixelFormat::RgbA32, size.width, size.height);
        *self.fb.lock() = Framebuffer::new_checked(size.width, size.height)?;
        let factory = result.activation_factory;
        let mut stage = ActiveStageBuilder {
            static_channels: result.static_channels,
            user_channel_id: result.user_channel_id,
            io_channel_id: result.io_channel_id,
            message_channel_id: result.message_channel_id,
            share_id: result.share_id,
            compression_type: result.compression_type,
            enable_server_pointer: result.enable_server_pointer,
            pointer_software_rendering: result.pointer_software_rendering,
        }
        .build();
        let mut input = Database::new();
        let clip_rx = self.clip_rx.lock().take();
        // A resize waits here until the Display Control channel is open (it opens after logon).
        let mut want_size: Option<(u16, u16)> = None;
        loop {
            let mut outputs = Vec::new();
            loop {
                match rx.try_recv() {
                    Ok(Input::Ops(ops)) => {
                        let events = input.apply(ops);
                        if !events.is_empty() {
                            outputs.extend(stage.process_fastpath_input(&mut image, &events)?);
                        }
                    }
                    Ok(Input::Resize(w, h)) => want_size = Some((w, h)).filter(|&s| s != (image.width(), image.height())),
                    Ok(Input::Clipboard(text)) => {
                        *self.clip_text.lock() = Some(text);
                        if let Some(c) = stage.get_svc_processor_mut::<CliprdrClient>()
                            && let Ok(msgs) = c.initiate_copy(&[ClipboardFormat::new(ClipboardFormatId::CF_UNICODETEXT)])
                        {
                            outputs.push(ActiveStageOutput::ResponseFrame(stage.process_svc_processor_messages(msgs)?));
                        }
                    }
                    Ok(Input::Close) | Err(mpsc::TryRecvError::Disconnected) => {
                        for o in stage.graceful_shutdown()? {
                            if let ActiveStageOutput::ResponseFrame(f) = o {
                                let _ = framed.write_all(&f);
                            }
                        }
                        return Ok(());
                    }
                    Err(mpsc::TryRecvError::Empty) => break,
                }
            }
            // Requests the clipboard backend queued while the stage processed server PDUs.
            for msg in clip_rx.iter().flat_map(|rx| rx.try_iter()) {
                let Some(c) = stage.get_svc_processor_mut::<CliprdrClient>() else { break };
                let msgs = match msg {
                    ClipboardMessage::SendInitiateCopy(formats) => c.initiate_copy(&formats),
                    ClipboardMessage::SendFormatData(response) => c.submit_format_data(response),
                    ClipboardMessage::SendInitiatePaste(format) => c.initiate_paste(format),
                    _ => continue,
                };
                if let Ok(msgs) = msgs {
                    outputs.push(ActiveStageOutput::ResponseFrame(stage.process_svc_processor_messages(msgs)?));
                }
            }
            if let Some((w, h)) = want_size {
                let (w, h) = ironrdp::displaycontrol::pdu::MonitorLayoutEntry::adjust_display_size(w.into(), h.into());
                if let Some(frame) = stage.encode_resize(w, h, None, None) {
                    outputs.push(ActiveStageOutput::ResponseFrame(frame?));
                    want_size = None;
                }
            }
            match framed.read_pdu() {
                Ok((action, payload)) => outputs.extend(stage.process(&mut image, action, &payload)?),
                Err(e) if matches!(e.kind(), std::io::ErrorKind::WouldBlock | std::io::ErrorKind::TimedOut) => {}
                Err(e) => return Err(format!("RDP connection lost: {e}").into()),
            }
            for out in outputs {
                match out {
                    ActiveStageOutput::ResponseFrame(frame) => framed.write_all(&frame)?,
                    ActiveStageOutput::GraphicsUpdate(r) => {
                        copy_rect(&image, &mut self.fb.lock(), r.left, r.top, r.right, r.bottom);
                        (self.cfg.on_frame)();
                    }
                    ActiveStageOutput::DeactivateAll => {
                        // Deactivation-Reactivation (e.g. after a resize): new desktop size and channel state.
                        framed.get_inner_mut().0.sock.set_read_timeout(Some(self.cfg.connect_timeout.max(Duration::from_secs(5))))?;
                        let mut seq = factory.create();
                        let mut buf = ironrdp::core::WriteBuf::new();
                        loop {
                            sequence_step(&mut framed, &mut seq, &mut buf).map_err(|e| format!("RDP reactivation failed: {e}"))?;
                            if let ConnectionActivationState::Finalized { desktop_size, share_id, enable_server_pointer, pointer_software_rendering } =
                                seq.connection_activation_state()
                            {
                                image = DecodedImage::new(PixelFormat::RgbA32, desktop_size.width, desktop_size.height);
                                *self.fb.lock() = Framebuffer::new_checked(desktop_size.width, desktop_size.height)?;
                                stage.set_fastpath_processor(
                                    fast_path::ProcessorBuilder {
                                        io_channel_id: seq.io_channel_id(),
                                        user_channel_id: seq.user_channel_id(),
                                        share_id,
                                        enable_server_pointer,
                                        pointer_software_rendering,
                                        bulk_decompressor: None,
                                    }
                                    .build(),
                                );
                                stage.set_share_id(share_id);
                                stage.set_enable_server_pointer(enable_server_pointer);
                                break;
                            }
                        }
                        framed.get_inner_mut().0.sock.set_read_timeout(Some(POLL))?;
                        (self.cfg.on_frame)();
                    }
                    ActiveStageOutput::Terminate(reason) => return Err(format!("RDP session ended: {reason}").into()),
                    _ => {}
                }
            }
        }
    }
}

/// Text-only clipboard sharing (CF_UNICODETEXT) over CLIPRDR. Callbacks run inside the active
/// stage, so they queue requests for the session loop instead of answering directly.
struct TextClipboard {
    tx: Sender<ClipboardMessage>,
    text: Arc<Mutex<Option<String>>>,
    on_text: ClipboardFn,
}

impl std::fmt::Debug for TextClipboard {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.write_str("TextClipboard")
    }
}

impl ironrdp::core::AsAny for TextClipboard {
    fn as_any(&self) -> &dyn std::any::Any {
        self
    }
    fn as_any_mut(&mut self) -> &mut dyn std::any::Any {
        self
    }
}

impl CliprdrBackend for TextClipboard {
    fn temporary_directory(&self) -> &str {
        ""
    }
    fn client_capabilities(&self) -> ClipboardGeneralCapabilityFlags {
        ClipboardGeneralCapabilityFlags::empty()
    }
    fn on_ready(&mut self) {}
    fn on_request_format_list(&mut self) {
        let formats = if self.text.lock().is_some() { vec![ClipboardFormat::new(ClipboardFormatId::CF_UNICODETEXT)] } else { vec![] };
        let _ = self.tx.send(ClipboardMessage::SendInitiateCopy(formats));
    }
    fn on_process_negotiated_capabilities(&mut self, _: ClipboardGeneralCapabilityFlags) {}
    fn on_remote_copy(&mut self, formats: &[ClipboardFormat]) {
        if formats.iter().any(|f| f.id() == ClipboardFormatId::CF_UNICODETEXT) {
            let _ = self.tx.send(ClipboardMessage::SendInitiatePaste(ClipboardFormatId::CF_UNICODETEXT));
        }
    }
    fn on_format_data_request(&mut self, request: FormatDataRequest) {
        let response = match &*self.text.lock() {
            Some(t) if request.format == ClipboardFormatId::CF_UNICODETEXT => FormatDataResponse::new_unicode_string(t),
            _ => FormatDataResponse::new_error(),
        };
        let _ = self.tx.send(ClipboardMessage::SendFormatData(ironrdp::core::IntoOwned::into_owned(response)));
    }
    fn on_format_data_response(&mut self, response: FormatDataResponse<'_>) {
        if !response.is_error()
            && let Ok(text) = response.to_unicode_string()
        {
            (self.on_text)(text);
        }
    }
    fn on_file_contents_request(&mut self, _: FileContentsRequest) {}
    fn on_file_contents_response(&mut self, _: FileContentsResponse<'_>) {}
    fn on_lock(&mut self, _: LockDataId) {}
    fn on_unlock(&mut self, _: LockDataId) {}
}

/// One step of a connection sequence: read the PDU it waits for (if any), answer if it says to.
fn sequence_step(framed: &mut Framed<Tls>, seq: &mut dyn Sequence, buf: &mut ironrdp::core::WriteBuf) -> Result<()> {
    buf.clear();
    let written = match seq.next_pdu_hint() {
        Some(hint) => {
            let pdu = framed.read_by_hint(hint)?;
            seq.step(&pdu, buf)?
        }
        None => seq.step_no_input(buf)?,
    };
    if written.size().is_some() {
        framed.write_all(buf.filled())?;
    }
    Ok(())
}

/// Copies an updated rectangle (inclusive bounds) from the decoder's image into the shared
/// framebuffer, forcing opaque alpha (RDP leaves it undefined).
fn copy_rect(image: &DecodedImage, fb: &mut Framebuffer, left: u16, top: u16, right: u16, bottom: u16) {
    if (fb.width, fb.height) != (image.width(), image.height()) {
        return;
    }
    let (w, stride) = (fb.width as usize, image.stride());
    let (x0, x1) = (left as usize, (right as usize).min(w.saturating_sub(1)));
    let (y0, y1) = (top as usize, (bottom as usize).min(fb.height as usize - 1));
    if x0 > x1 || y0 > y1 {
        return;
    }
    let data = image.data();
    for y in y0..=y1 {
        let (s, d) = (y * stride + x0 * 4, (y * w + x0) * 4);
        let n = (x1 - x0 + 1) * 4;
        fb.rgba[d..d + n].copy_from_slice(&data[s..s + n]);
        fb.rgba[d..d + n].as_chunks_mut::<4>().0.iter_mut().for_each(|p| p[3] = 255);
    }
    fb.dirty = true;
}

/// A transport through an RD Gateway (MS-TSGU over HTTPS WebSocket, Basic authentication). The
/// gateway's certificate is verified against the web PKI. The async gateway stream is bridged to
/// a loopback socket so the blocking RDP client can use it like any other connection.
pub fn gateway_dial(endpoint: String, user: String, password: zeroize::Zeroizing<String>, server: String, port: u16) -> Dial {
    Arc::new(move || {
        let endpoint = if endpoint.contains(':') { endpoint.clone() } else { format!("{endpoint}:443") };
        let target = ironrdp_mstsgu::GwConnectTarget {
            gw_endpoint: endpoint,
            gw_user: user.clone(),
            gw_pass: password.to_string(),
            server: server.clone(),
            server_port: port,
        };
        let (gw, _) = super::rt()
            .block_on(ironrdp_mstsgu::GwClient::connect(&target, "Nexus Terminal"))
            .map_err(|e| format!("RD Gateway {}: {}", target.gw_endpoint, error_chain(&e)))?;
        let (client, server_end) = super::ssh::loopback_pair()?;
        server_end.set_nonblocking(true)?;
        super::rt().spawn(async move {
            if let Ok(mut local) = tokio::net::TcpStream::from_std(server_end) {
                let mut gw = gw;
                let _ = tokio::io::copy_bidirectional(&mut local, &mut gw).await;
            }
        });
        Ok(client)
    })
}

/// `SHA256:<unpadded base64>` of the certificate, the format SSH host keys use.
pub fn fingerprint(cert_der: &[u8]) -> String {
    use base64::Engine;
    format!("SHA256:{}", base64::engine::general_purpose::STANDARD_NO_PAD.encode(Sha256::digest(cert_der)))
}

fn server_public_key(cert_der: &[u8]) -> Result<Vec<u8>> {
    use x509_cert::der::Decode;
    let cert = x509_cert::Certificate::from_der(cert_der).map_err(|e| format!("RDP server certificate is unreadable: {e}"))?;
    Ok(cert.tbs_certificate.subject_public_key_info.subject_public_key.as_bytes().ok_or("RDP server public key is malformed")?.to_vec())
}

/// TLS without CA validation (RDP servers use self-signed certificates): the handshake still
/// proves possession of the certificate's key, and the caller pins its fingerprint.
fn tls_upgrade(mut tcp: TcpStream, host: &str) -> Result<(Tls, Vec<u8>)> {
    let provider = Arc::new(rustls::crypto::ring::default_provider());
    let config = rustls::ClientConfig::builder_with_provider(provider.clone())
        .with_safe_default_protocol_versions()?
        .dangerous()
        .with_custom_certificate_verifier(Arc::new(Pinned(provider)))
        .with_no_client_auth();
    let name = rustls::pki_types::ServerName::try_from(host.to_string()).map_err(|e| format!("invalid RDP host name {host:?}: {e}"))?;
    let mut conn = rustls::ClientConnection::new(Arc::new(config), name)?;
    while conn.is_handshaking() {
        conn.complete_io(&mut tcp).map_err(|e| format!("RDP TLS handshake failed: {e}"))?;
    }
    let cert = conn.peer_certificates().and_then(|c| c.first()).ok_or("RDP server sent no certificate")?.to_vec();
    Ok((rustls::StreamOwned::new(conn, tcp), cert))
}

#[derive(Debug)]
struct Pinned(Arc<rustls::crypto::CryptoProvider>);

impl rustls::client::danger::ServerCertVerifier for Pinned {
    fn verify_server_cert(
        &self,
        _: &rustls::pki_types::CertificateDer<'_>,
        _: &[rustls::pki_types::CertificateDer<'_>],
        _: &rustls::pki_types::ServerName<'_>,
        _: &[u8],
        _: rustls::pki_types::UnixTime,
    ) -> std::result::Result<rustls::client::danger::ServerCertVerified, rustls::Error> {
        Ok(rustls::client::danger::ServerCertVerified::assertion())
    }

    fn verify_tls12_signature(
        &self,
        message: &[u8],
        cert: &rustls::pki_types::CertificateDer<'_>,
        dss: &rustls::DigitallySignedStruct,
    ) -> std::result::Result<rustls::client::danger::HandshakeSignatureValid, rustls::Error> {
        rustls::crypto::verify_tls12_signature(message, cert, dss, &self.0.signature_verification_algorithms)
    }

    fn verify_tls13_signature(
        &self,
        message: &[u8],
        cert: &rustls::pki_types::CertificateDer<'_>,
        dss: &rustls::DigitallySignedStruct,
    ) -> std::result::Result<rustls::client::danger::HandshakeSignatureValid, rustls::Error> {
        rustls::crypto::verify_tls13_signature(message, cert, dss, &self.0.signature_verification_algorithms)
    }

    fn supported_verify_schemes(&self) -> Vec<rustls::SignatureScheme> {
        self.0.signature_verification_algorithms.supported_schemes()
    }
}

/// CredSSP asks for this only for Kerberos (talking to a KDC); NTLM never uses it.
struct NoNetwork;

impl connector::sspi::network_client::NetworkClient for NoNetwork {
    fn send(&self, _: &connector::sspi::NetworkRequest) -> connector::sspi::Result<Vec<u8>> {
        Err(connector::sspi::Error::new(connector::sspi::ErrorKind::NoAuthenticatingAuthority, "Kerberos is not supported; use NTLM"))
    }
}

/// IronRDP errors carry their cause in a chain; surface the useful part.
fn describe(e: &connector::ConnectorError) -> String {
    error_chain(e)
}

fn error_chain(e: &dyn std::error::Error) -> String {
    let mut s = e.to_string();
    let mut src = e.source();
    while let Some(c) = src {
        s = format!("{s}: {c}");
        src = c.source();
    }
    s
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn fingerprints_like_ssh() {
        assert_eq!(fingerprint(b"abc"), "SHA256:ungWv48Bz+pBQUDeXa4iI7ADYaOWF3qctBD/YfIAFa0");
    }

    #[test]
    fn copies_inclusive_rectangles_with_opaque_alpha() {
        let mut image = DecodedImage::new(PixelFormat::RgbA32, 4, 3);
        let mut fb = Framebuffer::new_checked(4, 3).unwrap();
        fb.rgba.fill(0);
        copy_rect(&image, &mut fb, 1, 1, 2, 1);
        let alpha: Vec<u8> = fb.rgba.chunks(4).map(|p| p[3]).collect();
        assert_eq!(alpha, [0, 0, 0, 0, 0, 255, 255, 0, 0, 0, 0, 0]);
        // Out-of-range bounds are clipped, a size mismatch is ignored.
        copy_rect(&image, &mut fb, 3, 2, 99, 99);
        assert_eq!(fb.rgba[(2 * 4 + 3) * 4 + 3], 255);
        image = DecodedImage::new(PixelFormat::RgbA32, 8, 8);
        copy_rect(&image, &mut fb, 0, 0, 7, 7);
        assert_eq!(fb.rgba[3], 0);
    }

    /// `NEXUS_RDP_TEST="host port user password" cargo test -p nexus-core rdp::tests::live -- --ignored --nocapture`
    #[test]
    #[ignore]
    fn live() {
        let spec = std::env::var("NEXUS_RDP_TEST").expect("NEXUS_RDP_TEST");
        let v: Vec<&str> = spec.split_whitespace().collect();
        let (host, port) = (v[0].to_string(), v[1].parse::<u16>().unwrap());
        let frames = Arc::new(std::sync::atomic::AtomicUsize::new(0));
        let (f2, h2) = (frames.clone(), host.clone());
        let seen_cert = Arc::new(Mutex::new(String::new()));
        let sc = seen_cert.clone();
        let c = Client::new(Config {
            host: host.clone(),
            username: v[2].into(),
            domain: String::new(),
            password: zeroize::Zeroizing::new(v[3].into()),
            width: 1024,
            height: 768,
            allow_tls_only: true,
            audio: std::env::var("NEXUS_RDP_AUDIO").is_ok(),
            shared_folder: std::env::var("NEXUS_RDP_SHARE").ok().map(|p| (p.into(), "nexus".into())),
            printer: std::env::var("NEXUS_RDP_PRINT").ok().map(|p| (p.into(), Arc::new(|f: std::path::PathBuf| eprintln!("printed {}", f.display())) as _)),
            dial: Arc::new(move || Ok(TcpStream::connect((h2.as_str(), port))?)),
            check_cert: Arc::new(move |fp| {
                *sc.lock() = fp.to_string();
                Ok(())
            }),
            connect_timeout: Duration::from_secs(15),
            on_frame: Arc::new(move || {
                f2.fetch_add(1, std::sync::atomic::Ordering::SeqCst);
            }),
            on_clipboard: Arc::new(|t| eprintln!("remote clipboard: {t:?}")),
            on_error: Arc::new(|e| eprintln!("on_error: {e}")),
            on_close: Arc::new(|| eprintln!("on_close")),
        });
        c.connect().unwrap();
        eprintln!("cert {}", seen_cert.lock());
        let t = std::time::Instant::now();
        while frames.load(std::sync::atomic::Ordering::SeqCst) < 5 && t.elapsed() < Duration::from_secs(20) {
            std::thread::sleep(Duration::from_millis(100));
        }
        c.input(vec![Operation::MouseMove(MousePosition { x: 100, y: 100 })]);
        c.input(vec![Operation::KeyPressed(Scancode::from_u8(false, 0x1e)), Operation::KeyReleased(Scancode::from_u8(false, 0x1e))]);
        std::thread::sleep(Duration::from_secs(2));
        if std::env::var("NEXUS_RDP_CLIP").is_ok() {
            c.set_clipboard("from-nexus-123".into());
            std::thread::sleep(Duration::from_secs(25));
        }
        let fb = c.framebuffer().lock().clone();
        eprintln!("frames {} size {}x{}", frames.load(std::sync::atomic::Ordering::SeqCst), fb.width, fb.height);
        if let Ok(out) = std::env::var("NEXUS_RDP_PNG") {
            image::DynamicImage::ImageRgba8(image::RgbaImage::from_raw(fb.width.into(), fb.height.into(), fb.rgba.clone()).unwrap())
                .to_rgb8()
                .save(out)
                .unwrap();
        }
        assert!(frames.load(std::sync::atomic::Ordering::SeqCst) > 0);
        c.close();
    }
}
