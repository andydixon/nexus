//! Native VNC (RFB 3.3/3.7/3.8) client: None and VNC Authentication, Raw, CopyRect, Hextile, ZRLE
//! and Tight (zlib + JPEG), plus the DesktopSize, ExtendedDesktopSize and LastRect pseudo-encodings.
//!
//! One I/O thread owns the transport (TCP, or a caller-supplied stream such as an SSH tunnel). It
//! decodes into a private framebuffer and, after each FramebufferUpdate, copies the changed rows into
//! the shared `Arc<Mutex<Framebuffer>>` the UI reads, so the lock is never held across network reads.
//! Outgoing input is queued and written whenever a read polls out, so the transport is never split.

use parking_lot::{Mutex, MutexGuard};
use std::io::{BufReader, ErrorKind, Read, Write};
use std::sync::Arc;
use std::sync::mpsc::{self, Receiver, Sender, TryRecvError};
use std::time::Duration;

use flate2::{Decompress, FlushDecompress};

pub use super::irc::{Connector, ReadWrite};
use super::{CloseFn, ErrorFn};

const READ_POLL: Duration = Duration::from_millis(50);
/// Largest desktop accepted from the server (64 Mpx = 256 MiB of RGBA; an 8K display is 33 Mpx).
const MAX_FB_PIXELS: usize = 64 << 20;
/// Largest single compressed rectangle (ZRLE) read from the server.
const MAX_ZRLE_BYTES: usize = 64 << 20;

pub type FrameFn = Arc<dyn Fn() + Send + Sync>;
pub type ResizeFn = Arc<dyn Fn(u16, u16) + Send + Sync>;
pub type ClipboardFn = Arc<dyn Fn(String) + Send + Sync>;

#[derive(Clone)]
pub struct Config {
    pub host: String,
    pub port: u16,
    pub password: Option<String>,
    /// ClientInit's shared flag: leave other viewers connected.
    pub shared: bool,
    pub view_only: bool,
    /// Tight JPEG quality, 0–9.
    pub quality: u8,
    /// Provides the transport instead of a direct TCP connect (e.g. an SSH direct-tcpip stream).
    pub connector: Option<Connector>,
    pub connect_timeout: Duration,
    /// Called after each FramebufferUpdate has been applied to the shared framebuffer.
    pub on_frame: FrameFn,
    pub on_resize: ResizeFn,
    pub on_clipboard: ClipboardFn,
    pub on_error: ErrorFn,
    pub on_close: CloseFn,
}

impl Default for Config {
    fn default() -> Config {
        Config {
            host: String::new(),
            port: 5900,
            password: None,
            shared: true,
            view_only: false,
            quality: 6,
            connector: None,
            connect_timeout: Duration::ZERO,
            on_frame: Arc::new(|| {}),
            on_resize: Arc::new(|_, _| {}),
            on_clipboard: Arc::new(|_| {}),
            on_error: Arc::new(|_| {}),
            on_close: Arc::new(|| {}),
        }
    }
}

/// The remote desktop as RGBA, row-major, `width * height * 4` bytes. The I/O thread sets `dirty`
/// after each update; the UI clears it once it has uploaded the texture.
#[derive(Clone, Debug, Default)]
pub struct Framebuffer {
    pub width: u16,
    pub height: u16,
    pub rgba: Vec<u8>,
    pub dirty: bool,
}

impl Framebuffer {
    fn new(width: u16, height: u16) -> Framebuffer {
        let mut rgba = vec![0u8; width as usize * height as usize * 4];
        rgba.as_chunks_mut::<4>().0.iter_mut().for_each(|p| p[3] = 255);
        Framebuffer { width, height, rgba, dirty: true }
    }

    /// A framebuffer of a server-announced size, refusing sizes that would exhaust memory.
    fn new_checked(width: u16, height: u16) -> crate::Result<Framebuffer> {
        if width == 0 || height == 0 || width as usize * height as usize > MAX_FB_PIXELS {
            return Err(format!("unsupported desktop size {width}x{height}").into());
        }
        Ok(Framebuffer::new(width, height))
    }

    fn check(&self, x: u16, y: u16, w: u16, h: u16) -> crate::Result<()> {
        if x as usize + w as usize > self.width as usize || y as usize + h as usize > self.height as usize {
            return Err(format!("rectangle {w}x{h}+{x}+{y} outside the {}x{} framebuffer", self.width, self.height).into());
        }
        Ok(())
    }

    /// Writes `w*h` pixels of `bpp` bytes each (R, G, B first; anything after is ignored).
    fn put(&mut self, x: u16, y: u16, w: u16, h: u16, px: &[u8], bpp: usize) {
        let (w, stride) = (w as usize, self.width as usize);
        for row in 0..h as usize {
            let dst = ((y as usize + row) * stride + x as usize) * 4;
            for (d, s) in self.rgba[dst..dst + w * 4].as_chunks_mut::<4>().0.iter_mut().zip(px[row * w * bpp..].chunks_exact(bpp)) {
                d[..3].copy_from_slice(&s[..3]);
                d[3] = 255;
            }
        }
    }

    fn fill(&mut self, x: u16, y: u16, w: u16, h: u16, rgb: &[u8]) {
        let stride = self.width as usize;
        for row in 0..h as usize {
            let dst = ((y as usize + row) * stride + x as usize) * 4;
            for d in self.rgba[dst..dst + w as usize * 4].as_chunks_mut::<4>().0 {
                d[..3].copy_from_slice(&rgb[..3]);
                d[3] = 255;
            }
        }
    }
}

struct State {
    connected: bool,
    closing: bool,
    ready: Option<Sender<Result<(), String>>>,
    name: String,
}

pub struct Client {
    cfg: Config,
    fb: Arc<Mutex<Framebuffer>>,
    state: Mutex<State>,
    out: Mutex<Option<Sender<Vec<u8>>>>,
}

impl Client {
    pub fn new(mut cfg: Config) -> Arc<Client> {
        if cfg.connect_timeout.is_zero() {
            cfg.connect_timeout = Duration::from_secs(30);
        }
        cfg.quality = cfg.quality.min(9);
        Arc::new(Client {
            cfg,
            fb: Arc::new(Mutex::new(Framebuffer::default())),
            state: Mutex::new(State { connected: false, closing: false, ready: None, name: String::new() }),
            out: Mutex::new(None),
        })
    }

    fn st(&self) -> MutexGuard<'_, State> {
        self.state.lock()
    }

    /// Connects and blocks until the handshake completes (ServerInit received), fails, or times out.
    pub fn connect(self: &Arc<Self>) -> crate::Result<()> {
        let (ready_tx, ready_rx) = mpsc::channel();
        let (out_tx, out_rx) = mpsc::channel();
        self.st().ready = Some(ready_tx);
        *self.out.lock() = Some(out_tx);

        let client = self.clone();
        std::thread::spawn(move || client.io_thread(out_rx));

        let timeout = self.cfg.connect_timeout;
        match ready_rx.recv_timeout(timeout) {
            Ok(Ok(())) => Ok(()),
            Ok(Err(e)) => Err(e.into()),
            Err(_) => {
                self.close();
                Err(format!("VNC connection timed out after {timeout:?}").into())
            }
        }
    }

    /// Disconnects. Idempotent; `on_close` fires from the I/O thread.
    pub fn close(&self) {
        {
            let mut st = self.st();
            if st.closing {
                return;
            }
            st.closing = true;
        }
        // Dropping the sender makes the I/O thread's next poll end the connection.
        *self.out.lock() = None;
    }

    pub fn framebuffer(&self) -> Arc<Mutex<Framebuffer>> {
        self.fb.clone()
    }

    pub fn desktop_name(&self) -> String {
        self.st().name.clone()
    }

    /// PointerEvent. `buttons` bit 0 is left, 1 middle, 2 right, 3/4 wheel up/down.
    pub fn pointer(&self, x: u16, y: u16, buttons: u8) -> crate::Result<()> {
        if self.cfg.view_only {
            return Ok(());
        }
        let mut m = vec![5, buttons];
        m.extend_from_slice(&x.to_be_bytes());
        m.extend_from_slice(&y.to_be_bytes());
        self.queue(m)
    }

    /// KeyEvent with an X11 keysym (see `keysym_for`).
    pub fn key(&self, keysym: u32, down: bool) -> crate::Result<()> {
        if self.cfg.view_only {
            return Ok(());
        }
        let mut m = vec![4, down as u8, 0, 0];
        m.extend_from_slice(&keysym.to_be_bytes());
        self.queue(m)
    }

    /// ClientCutText. RFB clipboard text is Latin-1; other characters become `?`.
    pub fn send_clipboard(&self, text: &str) -> crate::Result<()> {
        if self.cfg.view_only {
            return Ok(());
        }
        let bytes: Vec<u8> = text.chars().map(|c| u8::try_from(c).unwrap_or(b'?')).collect();
        let mut m = vec![6, 0, 0, 0];
        m.extend_from_slice(&(bytes.len() as u32).to_be_bytes());
        m.extend_from_slice(&bytes);
        self.queue(m)
    }

    /// Asks for a non-incremental update of the whole screen.
    pub fn request_full_update(&self) -> crate::Result<()> {
        let (w, h) = {
            let fb = self.fb.lock();
            (fb.width, fb.height)
        };
        self.queue(update_request(false, w, h))
    }

    fn queue(&self, msg: Vec<u8>) -> crate::Result<()> {
        let guard = self.out.lock();
        if guard.as_ref().is_some_and(|tx| tx.send(msg).is_ok()) { Ok(()) } else { Err("not connected".into()) }
    }

    fn io_thread(self: Arc<Self>, out_rx: Receiver<Vec<u8>>) {
        let result = self.open_transport().and_then(|io| self.run(io, out_rx));
        *self.out.lock() = None;
        let (was_closing, was_connected, ready) = {
            let mut st = self.st();
            let r = (st.closing, st.connected, st.ready.take());
            st.connected = false;
            r
        };
        if let Err(e) = result
            && !was_closing
        {
            let msg = e.to_string();
            (self.cfg.on_error)(msg.clone());
            if let Some(ready) = ready {
                let _ = ready.send(Err(msg));
            }
        }
        if was_connected {
            (self.cfg.on_close)();
        }
    }

    fn open_transport(&self) -> crate::Result<Box<dyn ReadWrite>> {
        if let Some(connector) = &self.cfg.connector {
            return connector();
        }
        let (addr, tcp) = super::dial(&self.cfg.host, self.cfg.port as i64, self.cfg.connect_timeout.as_secs().max(1) as i64);
        let tcp = tcp.map_err(|e| format!("dial tcp {addr}: {e}"))?;
        tcp.set_read_timeout(Some(READ_POLL))?;
        tcp.set_write_timeout(Some(self.cfg.connect_timeout))?;
        let _ = tcp.set_nodelay(true);
        Ok(Box::new(tcp))
    }

    fn run(&self, io: Box<dyn ReadWrite>, out_rx: Receiver<Vec<u8>>) -> crate::Result<()> {
        let mut r = BufReader::with_capacity(1 << 16, Conn { io, out_rx });
        let (w, h) = self.handshake(&mut r)?;
        let mut local = Framebuffer::new_checked(w, h)?;
        *self.fb.lock() = local.clone();
        (self.cfg.on_resize)(w, h);
        {
            let mut st = self.st();
            if st.closing {
                return Ok(());
            }
            st.connected = true;
            if let Some(ready) = st.ready.take() {
                let _ = ready.send(Ok(()));
            }
        }
        let mut dec = Decoders::default();
        r.get_mut().send(&update_request(false, w, h))?;
        loop {
            match read_u8(&mut r)? {
                0 => {
                    let (y0, y1, resized) = read_update(&mut r, &mut local, &mut dec)?;
                    {
                        let mut fb = self.fb.lock();
                        if resized {
                            *fb = local.clone();
                        } else if y0 < y1 {
                            let row = local.width as usize * 4;
                            fb.rgba[y0 * row..y1 * row].copy_from_slice(&local.rgba[y0 * row..y1 * row]);
                        }
                        fb.dirty = true;
                    }
                    if resized {
                        (self.cfg.on_resize)(local.width, local.height);
                    }
                    (self.cfg.on_frame)();
                    r.get_mut().send(&update_request(!resized, local.width, local.height))?;
                }
                1 => {
                    // SetColourMapEntries: unused with true colour.
                    let hdr = read_n(&mut r, 5)?;
                    read_n(&mut r, u16::from_be_bytes([hdr[3], hdr[4]]) as usize * 6)?;
                }
                2 => {} // Bell
                3 => {
                    read_n(&mut r, 3)?;
                    let len = read_u32(&mut r)? as usize;
                    if len > 16 << 20 {
                        return Err("server clipboard text too long".into());
                    }
                    let text: String = read_n(&mut r, len)?.iter().map(|&b| b as char).collect();
                    (self.cfg.on_clipboard)(text);
                }
                t => return Err(format!("unsupported server message type {t}").into()),
            }
        }
    }

    /// ProtocolVersion, security, ClientInit/ServerInit, SetPixelFormat and SetEncodings.
    fn handshake(&self, r: &mut BufReader<Conn>) -> crate::Result<(u16, u16)> {
        let ver = read_n(r, 12)?;
        let ver = String::from_utf8_lossy(&ver);
        let minor = match ver.strip_prefix("RFB 003.").and_then(|s| s.trim_end().parse::<u32>().ok()) {
            Some(m) => m,
            None => return Err(format!("not a VNC server (got {:?})", ver.trim_end()).into()),
        };
        // 3.889 is Apple's 3.8; anything between 3.3 and 3.7 is treated as 3.3, as RFB specifies.
        let minor = if minor >= 8 {
            8
        } else if minor == 7 {
            7
        } else {
            3
        };
        r.get_mut().send(format!("RFB 003.00{minor}\n").as_bytes())?;

        let security = if minor == 3 {
            match read_u32(r)? {
                0 => return Err(format!("VNC server refused the connection: {}", read_string(r)?).into()),
                t => t,
            }
        } else {
            let n = read_u8(r)? as usize;
            if n == 0 {
                return Err(format!("VNC server refused the connection: {}", read_string(r)?).into());
            }
            let types = read_n(r, n)?;
            let pick = if types.contains(&1) && (self.cfg.password.is_none() || !types.contains(&2)) {
                1
            } else if types.contains(&2) {
                2
            } else {
                return Err(unsupported_security(&types).into());
            };
            r.get_mut().send(&[pick])?;
            pick as u32
        };

        match security {
            1 => {}
            2 => {
                let password = self.cfg.password.as_deref().ok_or("the VNC server requires a password")?;
                let challenge: [u8; 16] = read_n(r, 16)?.try_into().unwrap();
                r.get_mut().send(&vnc_auth_response(password, &challenge))?;
            }
            t => return Err(unsupported_security(&[t as u8]).into()),
        }
        if (minor == 8 || security == 2) && read_u32(r)? != 0 {
            let reason = if minor == 8 { read_string(r)? } else { String::new() };
            return Err(if reason.is_empty() {
                "VNC authentication failed (wrong password?)".into()
            } else {
                format!("VNC authentication failed: {reason}").into()
            });
        }

        r.get_mut().send(&[self.cfg.shared as u8])?;
        let init = read_n(r, 20)?;
        let (w, h) = (u16::from_be_bytes([init[0], init[1]]), u16::from_be_bytes([init[2], init[3]]));
        self.st().name = read_string(r)?;

        // 32bpp, depth 24, little-endian true colour with red in the low byte: pixels arrive as R,G,B,X.
        let mut m = vec![0, 0, 0, 0, 32, 24, 0, 1, 0, 255, 0, 255, 0, 255, 0, 8, 16, 0, 0, 0];
        let mut encodings: Vec<i32> = vec![7, 16, 5, 1, 0, -223, -308, -224];
        encodings.push(-32 + self.cfg.quality as i32); // JPEG quality level
        encodings.push(-256 + 2); // compression level 2, as the noVNC viewer used
        m.extend_from_slice(&[2, 0]);
        m.extend_from_slice(&(encodings.len() as u16).to_be_bytes());
        encodings.iter().for_each(|e| m.extend_from_slice(&e.to_be_bytes()));
        r.get_mut().send(&m)?;
        Ok((w, h))
    }
}

fn unsupported_security(types: &[u8]) -> String {
    if types.contains(&30) {
        return "the VNC server only offers Apple Remote Desktop authentication, which isn't supported; enable \"VNC viewers may control screen with password\" in Screen Sharing settings".into();
    }
    format!("the VNC server offers no supported security type (offered {types:?}; supported: None, VNC password)")
}

/// VNC Authentication: DES-encrypt the challenge with the password (first 8 bytes, zero padded)
/// as the key, each key byte bit-reversed.
pub fn vnc_auth_response(password: &str, challenge: &[u8; 16]) -> [u8; 16] {
    use des::cipher::{BlockCipherEncrypt, KeyInit};
    let mut key = [0u8; 8];
    for (k, b) in key.iter_mut().zip(password.bytes()) {
        *k = b.reverse_bits();
    }
    let cipher = des::Des::new(&key.into());
    let mut out = *challenge;
    for chunk in out.as_chunks_mut::<8>().0 {
        let mut block: [u8; 8] = *chunk;
        let b = (&mut block).into();
        cipher.encrypt_block(b);
        *chunk = block;
    }
    out
}

fn update_request(incremental: bool, w: u16, h: u16) -> Vec<u8> {
    let mut m = vec![3, incremental as u8, 0, 0, 0, 0];
    m.extend_from_slice(&w.to_be_bytes());
    m.extend_from_slice(&h.to_be_bytes());
    m
}

/// The transport as seen by the decoder: reads that poll out flush the outgoing queue and retry,
/// and a dropped queue (`Client::close`) ends the connection.
struct Conn {
    io: Box<dyn ReadWrite>,
    out_rx: Receiver<Vec<u8>>,
}

impl Conn {
    fn send(&mut self, data: &[u8]) -> std::io::Result<()> {
        self.io.write_all(data)?;
        self.io.flush()
    }
}

impl Read for Conn {
    fn read(&mut self, buf: &mut [u8]) -> std::io::Result<usize> {
        loop {
            match self.io.read(buf) {
                Ok(0) => return Err(std::io::Error::new(ErrorKind::UnexpectedEof, "connection closed by server")),
                Ok(n) => return Ok(n),
                Err(e) if matches!(e.kind(), ErrorKind::WouldBlock | ErrorKind::TimedOut | ErrorKind::Interrupted) => {}
                Err(e) => return Err(e),
            }
            loop {
                match self.out_rx.try_recv() {
                    Ok(msg) => self.io.write_all(&msg)?,
                    Err(TryRecvError::Empty) => break,
                    Err(TryRecvError::Disconnected) => return Err(std::io::Error::other("closed")),
                }
            }
            self.io.flush()?;
        }
    }
}

fn read_n(r: &mut impl Read, n: usize) -> crate::Result<Vec<u8>> {
    let mut v = vec![0u8; n];
    r.read_exact(&mut v)?;
    Ok(v)
}

fn read_u8(r: &mut impl Read) -> crate::Result<u8> {
    Ok(read_n(r, 1)?[0])
}

fn read_u16(r: &mut impl Read) -> crate::Result<u16> {
    let b = read_n(r, 2)?;
    Ok(u16::from_be_bytes([b[0], b[1]]))
}

fn read_u32(r: &mut impl Read) -> crate::Result<u32> {
    let b = read_n(r, 4)?;
    Ok(u32::from_be_bytes([b[0], b[1], b[2], b[3]]))
}

fn read_string(r: &mut impl Read) -> crate::Result<String> {
    let len = read_u32(r)? as usize;
    if len > 1 << 20 {
        return Err("server string too long".into());
    }
    Ok(String::from_utf8_lossy(&read_n(r, len)?).into_owned())
}

/// Persistent zlib streams: ZRLE has one, Tight four.
struct Decoders {
    zrle: Decompress,
    tight: [Decompress; 4],
    buf: Vec<u8>,
}

impl Default for Decoders {
    fn default() -> Decoders {
        Decoders { zrle: Decompress::new(true), tight: std::array::from_fn(|_| Decompress::new(true)), buf: Vec::new() }
    }
}

/// Inflates one server chunk (which ends at a sync flush) into `out`.
fn inflate(z: &mut Decompress, mut input: &[u8], out: &mut Vec<u8>) -> crate::Result<()> {
    out.clear();
    loop {
        out.reserve(64 << 10);
        let (i0, o0) = (z.total_in(), z.total_out());
        z.decompress_vec(input, out, FlushDecompress::Sync).map_err(|e| format!("zlib: {e}"))?;
        input = &input[(z.total_in() - i0) as usize..];
        let stalled = z.total_in() == i0 && z.total_out() == o0;
        if (input.is_empty() && out.len() < out.capacity()) || stalled {
            return Ok(());
        }
    }
}

/// Reads one FramebufferUpdate (after the type byte) into `fb`. Returns the changed row range and
/// whether the desktop was resized.
fn read_update(r: &mut impl Read, fb: &mut Framebuffer, dec: &mut Decoders) -> crate::Result<(usize, usize, bool)> {
    read_u8(r)?;
    let n = read_u16(r)?;
    let (mut y0, mut y1, mut resized) = (usize::MAX, 0, false);
    for _ in 0..n {
        let h = read_n(r, 12)?;
        let be = |i: usize| u16::from_be_bytes([h[i], h[i + 1]]);
        let (x, y, w, ht) = (be(0), be(2), be(4), be(6));
        let enc = i32::from_be_bytes([h[8], h[9], h[10], h[11]]);
        match enc {
            -224 => break, // LastRect
            -223 | -308 => {
                if enc == -308 {
                    let screens = read_n(r, 4)?[0] as usize;
                    read_n(r, screens * 16)?;
                }
                // ExtendedDesktopSize y is the status; non-zero answers a SetDesktopSize we never send.
                if enc == -223 || y == 0 {
                    *fb = Framebuffer::new_checked(w, ht)?;
                    resized = true;
                }
                continue;
            }
            _ => {}
        }
        fb.check(x, y, w, ht)?;
        match enc {
            0 => {
                let px = read_n(r, w as usize * ht as usize * 4)?;
                fb.put(x, y, w, ht, &px, 4);
            }
            1 => {
                let (sx, sy) = (read_u16(r)?, read_u16(r)?);
                fb.check(sx, sy, w, ht)?;
                let row = w as usize * 4;
                let stride = fb.width as usize * 4;
                let src: Vec<u8> = (0..ht as usize)
                    .flat_map(|i| {
                        let o = (sy as usize + i) * stride + sx as usize * 4;
                        fb.rgba[o..o + row].to_vec()
                    })
                    .collect();
                fb.put(x, y, w, ht, &src, 4);
            }
            5 => hextile(r, fb, x, y, w, ht)?,
            7 => tight(r, fb, dec, x, y, w, ht)?,
            16 => {
                let len = read_u32(r)? as usize;
                if len > MAX_ZRLE_BYTES {
                    return Err(format!("ZRLE rectangle of {len} bytes exceeds {MAX_ZRLE_BYTES}").into());
                }
                let data = read_n(r, len)?;
                inflate(&mut dec.zrle, &data, &mut dec.buf)?;
                zrle(&dec.buf, fb, x, y, w, ht)?;
            }
            e => return Err(format!("unsupported encoding {e}").into()),
        }
        y0 = y0.min(y as usize);
        y1 = y1.max(y as usize + ht as usize);
    }
    Ok((y0.min(y1), y1, resized))
}

fn hextile(r: &mut impl Read, fb: &mut Framebuffer, x: u16, y: u16, w: u16, h: u16) -> crate::Result<()> {
    let (mut bg, mut fg) = ([0u8; 4], [0u8; 4]);
    for ty in (y..y + h).step_by(16) {
        for tx in (x..x + w).step_by(16) {
            let (tw, th) = (16.min(x + w - tx), 16.min(y + h - ty));
            let sub = read_u8(r)?;
            if sub & 1 != 0 {
                let px = read_n(r, tw as usize * th as usize * 4)?;
                fb.put(tx, ty, tw, th, &px, 4);
                continue;
            }
            if sub & 2 != 0 {
                bg.copy_from_slice(&read_n(r, 4)?);
            }
            if sub & 4 != 0 {
                fg.copy_from_slice(&read_n(r, 4)?);
            }
            fb.fill(tx, ty, tw, th, &bg);
            if sub & 8 != 0 {
                for _ in 0..read_u8(r)? {
                    let colour = if sub & 16 != 0 { read_n(r, 4)? } else { fg.to_vec() };
                    let b = read_n(r, 2)?;
                    let (sx, sy, sw, sh) = ((b[0] >> 4) as u16, (b[0] & 15) as u16, (b[1] >> 4) as u16 + 1, (b[1] & 15) as u16 + 1);
                    if sx + sw > tw || sy + sh > th {
                        return Err("hextile subrect outside its tile".into());
                    }
                    fb.fill(tx + sx, ty + sy, sw, sh, &colour);
                }
            }
        }
    }
    Ok(())
}

/// ZRLE: 64×64 tiles of 3-byte CPIXELs (R, G, B with our pixel format).
fn zrle(data: &[u8], fb: &mut Framebuffer, x: u16, y: u16, w: u16, h: u16) -> crate::Result<()> {
    let mut p = 0usize;
    let mut take = |n: usize| take(data, &mut p, n);
    for ty in (y..y + h).step_by(64) {
        for tx in (x..x + w).step_by(64) {
            let (tw, th) = (64.min(x + w - tx), 64.min(y + h - ty));
            let count = tw as usize * th as usize;
            let sub = take(1)?[0];
            let mut px = Vec::with_capacity(count * 3);
            match sub {
                0 => px.extend_from_slice(take(count * 3)?),
                1 => fb.fill(tx, ty, tw, th, take(3)?),
                2..=16 => {
                    let palette = take(sub as usize * 3)?.to_vec();
                    let bits = match sub {
                        2 => 1,
                        3..=4 => 2,
                        _ => 4,
                    };
                    let row_bytes = (tw as usize * bits).div_ceil(8);
                    let packed = take(row_bytes * th as usize)?;
                    for row in packed.chunks_exact(row_bytes) {
                        for i in 0..tw as usize {
                            let bit = i * bits;
                            let idx = (row[bit / 8] >> (8 - bits - bit % 8)) as usize & ((1 << bits) - 1);
                            px.extend_from_slice(palette.get(idx * 3..idx * 3 + 3).ok_or("ZRLE palette index out of range")?);
                        }
                    }
                }
                128 | 130..=255 => {
                    let palette = if sub == 128 { Vec::new() } else { take((sub as usize - 128) * 3)?.to_vec() };
                    while px.len() < count * 3 {
                        let (colour, run) = if sub == 128 {
                            (take(3)?.to_vec(), Some(()))
                        } else {
                            let b = take(1)?[0];
                            let i = (b & 127) as usize;
                            (palette.get(i * 3..i * 3 + 3).ok_or("ZRLE palette index out of range")?.to_vec(), (b & 128 != 0).then_some(()))
                        };
                        let mut len = 1;
                        if run.is_some() {
                            loop {
                                let b = take(1)?[0];
                                len += b as usize;
                                if b != 255 {
                                    break;
                                }
                            }
                        }
                        if px.len() + len * 3 > count * 3 {
                            return Err("ZRLE run overflows its tile".into());
                        }
                        (0..len).for_each(|_| px.extend_from_slice(&colour));
                    }
                }
                s => return Err(format!("invalid ZRLE subencoding {s}").into()),
            }
            if sub != 1 {
                fb.put(tx, ty, tw, th, &px, 3);
            }
        }
    }
    Ok(())
}

fn take<'a>(data: &'a [u8], p: &mut usize, n: usize) -> crate::Result<&'a [u8]> {
    let s = data.get(*p..*p + n).ok_or("truncated ZRLE data")?;
    *p += n;
    Ok(s)
}

fn compact_len(r: &mut impl Read) -> crate::Result<usize> {
    let mut len = 0usize;
    for i in 0..3 {
        let b = read_u8(r)? as usize;
        if i == 2 {
            return Ok(len | b << 14);
        }
        len |= (b & 127) << (7 * i);
        if b & 128 == 0 {
            break;
        }
    }
    Ok(len)
}

/// Tight: fill, JPEG, or basic compression (copy/palette/gradient filters over four zlib streams).
fn tight(r: &mut impl Read, fb: &mut Framebuffer, dec: &mut Decoders, x: u16, y: u16, w: u16, h: u16) -> crate::Result<()> {
    let ctl = read_u8(r)?;
    for (i, z) in dec.tight.iter_mut().enumerate() {
        if ctl & (1 << i) != 0 {
            *z = Decompress::new(true);
        }
    }
    let (wu, hu) = (w as usize, h as usize);
    match ctl >> 4 {
        8 => {
            let rgb = read_n(r, 3)?;
            fb.fill(x, y, w, h, &rgb);
            return Ok(());
        }
        9 => {
            let len = compact_len(r)?;
            let jpeg = read_n(r, len)?;
            let img = image::load_from_memory_with_format(&jpeg, image::ImageFormat::Jpeg).map_err(|e| format!("Tight JPEG: {e}"))?.to_rgb8();
            if img.width() as usize != wu || img.height() as usize != hu {
                return Err("Tight JPEG size doesn't match its rectangle".into());
            }
            fb.put(x, y, w, h, img.as_raw(), 3);
            return Ok(());
        }
        c if c & 8 != 0 => return Err(format!("unsupported Tight compression type {c:#x}").into()),
        _ => {}
    }
    let filter = if ctl & 0x40 != 0 { read_u8(r)? } else { 0 };
    let palette = if filter == 1 {
        let n = read_u8(r)? as usize + 1;
        read_n(r, n * 3)?
    } else {
        Vec::new()
    };
    let size = match (filter, palette.len() / 3) {
        (0 | 2, _) => wu * hu * 3,
        (1, 2) => wu.div_ceil(8) * hu,
        (1, _) => wu * hu,
        (f, _) => return Err(format!("unsupported Tight filter {f}").into()),
    };
    let data = if size < 12 {
        read_n(r, size)?
    } else {
        let len = compact_len(r)?;
        let compressed = read_n(r, len)?;
        inflate(&mut dec.tight[(ctl >> 4 & 3) as usize], &compressed, &mut dec.buf)?;
        std::mem::take(&mut dec.buf)
    };
    if data.len() < size {
        return Err("truncated Tight data".into());
    }
    let px: Vec<u8> = match filter {
        1 => {
            let mut px = Vec::with_capacity(wu * hu * 3);
            let mut colour = |i: usize| -> crate::Result<()> {
                px.extend_from_slice(palette.get(i * 3..i * 3 + 3).ok_or("Tight palette index out of range")?);
                Ok(())
            };
            if palette.len() == 6 {
                for row in data[..size].chunks_exact(wu.div_ceil(8)) {
                    for i in 0..wu {
                        colour((row[i / 8] >> (7 - i % 8)) as usize & 1)?;
                    }
                }
            } else {
                data[..size].iter().try_for_each(|&i| colour(i as usize))?;
            }
            px
        }
        2 => {
            let mut px = vec![0u8; size];
            for j in 0..hu {
                for i in 0..wu {
                    for c in 0..3 {
                        let at = |i: usize, j: usize| px[(j * wu + i) * 3 + c] as i32;
                        let left = if i > 0 { at(i - 1, j) } else { 0 };
                        let up = if j > 0 { at(i, j - 1) } else { 0 };
                        let ul = if i > 0 && j > 0 { at(i - 1, j - 1) } else { 0 };
                        let pred = (left + up - ul).clamp(0, 255) as u8;
                        px[(j * wu + i) * 3 + c] = pred.wrapping_add(data[(j * wu + i) * 3 + c]);
                    }
                }
            }
            px
        }
        _ => data,
    };
    fb.put(x, y, w, h, &px, 3);
    Ok(())
}

/// The X11 keysym for a key: a named key (DOM `KeyboardEvent.key` / egui names), otherwise the
/// typed character (Latin-1 maps directly, other Unicode to `0x01000000 | codepoint`).
pub fn keysym_for(name: &str, ch: Option<char>) -> Option<u32> {
    let named = match name {
        "Enter" | "Return" => 0xff0d,
        "Backspace" => 0xff08,
        "Tab" => 0xff09,
        "Escape" | "Esc" => 0xff1b,
        "ArrowLeft" | "Left" => 0xff51,
        "ArrowUp" | "Up" => 0xff52,
        "ArrowRight" | "Right" => 0xff53,
        "ArrowDown" | "Down" => 0xff54,
        "Home" => 0xff50,
        "End" => 0xff57,
        "PageUp" => 0xff55,
        "PageDown" => 0xff56,
        "Insert" => 0xff63,
        "Delete" => 0xffff,
        "Shift" | "ShiftLeft" => 0xffe1,
        "ShiftRight" => 0xffe2,
        "Control" | "Ctrl" | "ControlLeft" => 0xffe3,
        "ControlRight" => 0xffe4,
        "Alt" | "AltLeft" => 0xffe9,
        "AltRight" => 0xffea,
        "AltGraph" => 0xfe03,
        "Super" | "Meta" | "Command" | "OS" | "SuperLeft" | "MetaLeft" => 0xffeb,
        "SuperRight" | "MetaRight" => 0xffec,
        "CapsLock" => 0xffe5,
        "Space" => 0x20,
        _ => match name.strip_prefix('F').and_then(|n| n.parse::<u32>().ok()) {
            Some(n @ 1..=12) => 0xffbe + n - 1,
            _ => 0,
        },
    };
    if named != 0 {
        return Some(named);
    }
    let c = ch? as u32;
    match c {
        0x20..=0x7e | 0xa0..=0xff => Some(c),
        0..=0x1f | 0x7f..=0x9f => None,
        _ => Some(0x0100_0000 | c),
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::net::TcpListener;

    #[test]
    fn des_response_matches_known_vector() {
        // openssl enc -des-ecb -K 0e86ceceeef64e26 (bit-reversed "password") over "0123456789abcdef".
        let got = vnc_auth_response("password", b"0123456789abcdef");
        let want = [0x56, 0x45, 0xab, 0xeb, 0x5f, 0x1e, 0x64, 0x75, 0xe8, 0xfe, 0xb1, 0x1b, 0xeb, 0x66, 0xea, 0x19];
        assert_eq!(got, want);
        // Only the first 8 password bytes count.
        assert_eq!(vnc_auth_response("password-too-long", b"0123456789abcdef"), want);
    }

    #[test]
    fn keysyms() {
        assert_eq!(keysym_for("Enter", None), Some(0xff0d));
        assert_eq!(keysym_for("F1", None), Some(0xffbe));
        assert_eq!(keysym_for("F12", None), Some(0xffc9));
        assert_eq!(keysym_for("F13", None), None);
        assert_eq!(keysym_for("Control", None), Some(0xffe3));
        assert_eq!(keysym_for("ArrowDown", None), Some(0xff54));
        assert_eq!(keysym_for("a", Some('a')), Some(0x61));
        assert_eq!(keysym_for("", Some('é')), Some(0xe9));
        assert_eq!(keysym_for("", Some('€')), Some(0x0100_20ac));
        assert_eq!(keysym_for("", Some('\u{1}')), None);
    }

    #[test]
    fn compact_lengths() {
        assert_eq!(compact_len(&mut &[0x05][..]).unwrap(), 5);
        assert_eq!(compact_len(&mut &[0x90, 0x4e][..]).unwrap(), 10000);
        assert_eq!(compact_len(&mut &[0xff, 0xff, 0xff][..]).unwrap(), 4194303);
    }

    fn zlib(data: &[u8]) -> Vec<u8> {
        let mut z = flate2::write::ZlibEncoder::new(Vec::new(), flate2::Compression::default());
        z.write_all(data).unwrap();
        z.finish().unwrap()
    }

    fn rect(m: &mut Vec<u8>, x: u16, y: u16, w: u16, h: u16, enc: i32) {
        for v in [x, y, w, h] {
            m.extend_from_slice(&v.to_be_bytes());
        }
        m.extend_from_slice(&enc.to_be_bytes());
    }

    fn fake_server(listener: TcpListener, password: &'static str) -> std::thread::JoinHandle<Vec<u8>> {
        std::thread::spawn(move || {
            let (mut s, _) = listener.accept().unwrap();
            s.set_read_timeout(Some(Duration::from_secs(5))).unwrap();
            let read = |s: &mut std::net::TcpStream, n: usize| {
                let mut b = vec![0u8; n];
                s.read_exact(&mut b).unwrap();
                b
            };
            s.write_all(b"RFB 003.008\n").unwrap();
            assert_eq!(read(&mut s, 12), b"RFB 003.008\n");
            s.write_all(&[1, 2]).unwrap();
            assert_eq!(read(&mut s, 1), [2]);
            let challenge = *b"0123456789abcdef";
            s.write_all(&challenge).unwrap();
            if read(&mut s, 16) != vnc_auth_response(password, &challenge) {
                s.write_all(&[0, 0, 0, 1, 0, 0, 0, 14]).unwrap();
                s.write_all(b"wrong password").unwrap();
                return Vec::new();
            }
            s.write_all(&[0, 0, 0, 0]).unwrap();
            assert_eq!(read(&mut s, 1), [1]); // shared
            let mut init = vec![0, 4, 0, 4];
            init.extend_from_slice(&[32, 24, 0, 1, 0, 255, 0, 255, 0, 255, 16, 8, 0, 0, 0, 0]);
            init.extend_from_slice(&[0, 0, 0, 4]);
            init.extend_from_slice(b"test");
            s.write_all(&init).unwrap();
            let pf = read(&mut s, 20);
            assert_eq!(pf[0], 0);
            let enc_hdr = read(&mut s, 4);
            assert_eq!(enc_hdr[0], 2);
            read(&mut s, u16::from_be_bytes([enc_hdr[2], enc_hdr[3]]) as usize * 4);
            assert_eq!(read(&mut s, 10), update_request(false, 4, 4));

            let mut m = vec![0, 0, 0, 5];
            rect(&mut m, 0, 0, 2, 2, 0); // Raw: red, green / blue, white
            m.extend_from_slice(&[255, 0, 0, 0, 0, 255, 0, 0, 0, 0, 255, 0, 255, 255, 255, 0]);
            rect(&mut m, 2, 2, 2, 2, 1); // CopyRect from (0,0)
            m.extend_from_slice(&[0, 0, 0, 0]);
            rect(&mut m, 2, 0, 2, 2, 16); // ZRLE solid yellow
            let z = zlib(&[1, 255, 255, 0]);
            m.extend_from_slice(&(z.len() as u32).to_be_bytes());
            m.extend_from_slice(&z);
            rect(&mut m, 0, 2, 1, 2, 5); // Hextile background cyan
            m.extend_from_slice(&[2, 0, 255, 255, 0]);
            rect(&mut m, 1, 2, 1, 2, 7); // Tight fill magenta
            m.extend_from_slice(&[0x80, 255, 0, 255]);
            s.write_all(&m).unwrap();
            assert_eq!(read(&mut s, 10), update_request(true, 4, 4));
            read(&mut s, 8) // the key event
        })
    }

    #[test]
    fn handshake_auth_and_decoding_against_fake_server() {
        let listener = TcpListener::bind("127.0.0.1:0").unwrap();
        let port = listener.local_addr().unwrap().port();
        let server = fake_server(listener, "secret");
        let (frame_tx, frame_rx) = mpsc::channel();
        let (close_tx, close_rx) = mpsc::channel();
        let client = Client::new(Config {
            host: "127.0.0.1".into(),
            port,
            password: Some("secret".into()),
            connect_timeout: Duration::from_secs(5),
            on_frame: Arc::new(move || frame_tx.send(()).unwrap()),
            on_close: Arc::new(move || close_tx.send(()).unwrap()),
            ..Default::default()
        });
        client.connect().unwrap();
        assert_eq!(client.desktop_name(), "test");
        frame_rx.recv_timeout(Duration::from_secs(5)).unwrap();
        {
            let fb = client.framebuffer();
            let fb = fb.lock();
            assert_eq!((fb.width, fb.height), (4, 4));
            assert!(fb.dirty);
            let px = |x: usize, y: usize| &fb.rgba[(y * 4 + x) * 4..(y * 4 + x) * 4 + 4];
            assert_eq!(px(0, 0), [255, 0, 0, 255]);
            assert_eq!(px(1, 1), [255, 255, 255, 255]);
            assert_eq!(px(2, 2), [255, 0, 0, 255]); // copied red
            assert_eq!(px(3, 2), [0, 255, 0, 255]); // copied green
            assert_eq!(px(2, 3), [0, 0, 255, 255]); // copied blue
            assert_eq!(px(3, 1), [255, 255, 0, 255]); // ZRLE
            assert_eq!(px(0, 3), [0, 255, 255, 255]); // Hextile
            assert_eq!(px(1, 2), [255, 0, 255, 255]); // Tight
        }
        client.key(0xff0d, true).unwrap();
        assert_eq!(server.join().unwrap(), [4, 1, 0, 0, 0, 0, 0xff, 0x0d]);
        client.close();
        client.close();
        close_rx.recv_timeout(Duration::from_secs(5)).unwrap();
        assert!(close_rx.recv_timeout(Duration::from_millis(200)).is_err());
    }

    #[test]
    fn wrong_password_reports_reason() {
        let listener = TcpListener::bind("127.0.0.1:0").unwrap();
        let port = listener.local_addr().unwrap().port();
        let server = fake_server(listener, "secret");
        let client = Client::new(Config {
            host: "127.0.0.1".into(),
            port,
            password: Some("nope".into()),
            connect_timeout: Duration::from_secs(5),
            ..Default::default()
        });
        let err = client.connect().unwrap_err().to_string();
        assert_eq!(err, "VNC authentication failed: wrong password");
        server.join().unwrap();
    }

    #[test]
    fn zrle_and_tight_palettes_and_gradient() {
        let px = |fb: &Framebuffer, x: usize| fb.rgba[x * 4..x * 4 + 3].to_vec();
        let (red, blue) = ([255u8, 0, 0], [0u8, 0, 255]);

        // ZRLE packed palette (1 bit/pixel): blue, red, blue.
        let mut fb = Framebuffer::new(3, 1);
        zrle(&[2, 255, 0, 0, 0, 0, 255, 0b1010_0000], &mut fb, 0, 0, 3, 1).unwrap();
        assert_eq!([px(&fb, 0), px(&fb, 1), px(&fb, 2)], [blue.to_vec(), red.to_vec(), blue.to_vec()]);
        // ZRLE palette RLE: index 0 run of 2, then index 1 once.
        zrle(&[130, 255, 0, 0, 0, 0, 255, 0x80, 1, 1], &mut fb, 0, 0, 3, 1).unwrap();
        assert_eq!([px(&fb, 0), px(&fb, 1), px(&fb, 2)], [red.to_vec(), red.to_vec(), blue.to_vec()]);

        // Tight two-colour palette, uncompressed (< 12 bytes): red, blue, red.
        let mut dec = Decoders::default();
        let msg = [0x40, 1, 1, 255, 0, 0, 0, 0, 255, 0b0100_0000];
        tight(&mut &msg[..], &mut fb, &mut dec, 0, 0, 3, 1).unwrap();
        assert_eq!([px(&fb, 0), px(&fb, 1), px(&fb, 2)], [red.to_vec(), blue.to_vec(), red.to_vec()]);

        // Tight gradient filter over zlib stream 1: a flat 2x2 colour.
        let mut fb = Framebuffer::new(2, 2);
        let z = zlib(&[10, 20, 30, 0, 0, 0, 0, 0, 0, 0, 0, 0]);
        let mut msg = vec![0x50, 2, z.len() as u8];
        msg.extend_from_slice(&z);
        tight(&mut &msg[..], &mut fb, &mut dec, 0, 0, 2, 2).unwrap();
        assert!((0..4).all(|i| px(&fb, i) == [10, 20, 30]));
    }

    #[test]
    fn apple_only_security_is_explained() {
        assert!(unsupported_security(&[30, 33]).contains("Apple Remote Desktop"));
    }

    #[test]
    fn server_sizes_and_rect_lengths_are_bounded() {
        assert!(Framebuffer::new_checked(7680, 4320).is_ok());
        assert!(Framebuffer::new_checked(0, 10).is_err());
        assert!(Framebuffer::new_checked(u16::MAX, u16::MAX).is_err());
        // A DesktopSize pseudo-rect announcing a 65535x65535 desktop is refused before allocating.
        let mut m = vec![0, 0, 1];
        rect(&mut m, 0, 0, u16::MAX, u16::MAX, -223);
        let mut fb = Framebuffer::new(4, 4);
        let mut dec = Decoders::default();
        assert!(read_update(&mut &m[..], &mut fb, &mut dec).unwrap_err().to_string().contains("unsupported desktop size"));
        // A ZRLE rect claiming 4 GiB of data is refused before allocating.
        let mut m = vec![0, 0, 1];
        rect(&mut m, 0, 0, 4, 4, 16);
        m.extend_from_slice(&u32::MAX.to_be_bytes());
        assert!(read_update(&mut &m[..], &mut fb, &mut dec).unwrap_err().to_string().contains("exceeds"));
    }
}
