// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Andy Dixon

//! RDP device redirection: a shared folder as a drive, and a virtual printer whose jobs are saved
//! as PostScript files.
//!
//! The drive is confined to one folder. IronRDP's native backend joins the server's paths onto
//! the share without checking them, so a hostile server could reach `\..\..\.ssh\id_ed25519`.
//! Every path the server sends is checked here first: no `..` components, and after resolving
//! symlinks it must stay inside the share. Drives are Unix only (the native backend has no Windows
//! implementation); the printer works everywhere.

use ironrdp::core::impl_as_any;
use ironrdp::pdu::PduResult;
use ironrdp::rdpdr::RdpdrBackend;
use ironrdp::rdpdr::pdu::RdpdrPdu;
use ironrdp::rdpdr::pdu::efs::{
    ClientDriveQueryDirectoryResponse, ClientDriveSetInformationResponse, DeviceCloseResponse, DeviceControlRequest, DeviceCreateResponse, DeviceIoResponse,
    DeviceWriteResponse, FileInformationClass, Information, NtStatus, PrinterIoRequest, ServerDeviceAnnounceResponse, ServerDriveIoRequest,
};
use ironrdp::rdpdr::pdu::esc::{ScardCall, ScardIoCtlCode};
use ironrdp::svc::SvcMessage;
#[cfg(unix)]
use ironrdp_rdpdr_native::backend::NixRdpdrBackend;
use std::collections::HashMap;
use std::io::Write;
use std::path::{Path, PathBuf};

/// A print job may not grow past this (a hostile server could otherwise fill the disk).
const MAX_PRINT_JOB: u64 = 256 << 20;
/// Print jobs open at once; each holds a file descriptor.
const MAX_OPEN_PRINT_JOBS: usize = 4;
/// Largest drive read served in one request. The server picks the length (a u32), and the
/// native backend allocates it before reading; Windows clients never ask for more than ~1 MiB.
const MAX_DRIVE_READ: u32 = 8 << 20;

/// Called with the file a finished print job was saved to.
pub type PrintedFn = std::sync::Arc<dyn Fn(PathBuf) + Send + Sync>;

#[derive(Default)]
pub struct Devices {
    #[cfg(unix)]
    drive: Option<ConfinedDrive>,
    printer: Option<Spool>,
}

impl std::fmt::Debug for Devices {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.write_str("Devices")
    }
}

impl_as_any!(Devices);

impl Devices {
    #[cfg(unix)]
    pub fn with_drive(mut self, folder: &Path) -> std::io::Result<Devices> {
        self.drive = Some(ConfinedDrive::new(folder)?);
        Ok(self)
    }

    /// Print jobs go to `dir` as `Nexus print <time>.ps`.
    pub fn with_printer(mut self, dir: PathBuf, on_printed: PrintedFn) -> Devices {
        self.printer = Some(Spool { dir, on_printed, jobs: HashMap::new(), next_id: 1 });
        self
    }
}

struct Spool {
    dir: PathBuf,
    on_printed: PrintedFn,
    jobs: HashMap<u32, (std::fs::File, PathBuf, u64)>,
    next_id: u32,
}

impl Spool {
    fn handle(&mut self, req: PrinterIoRequest) -> PduResult<Vec<SvcMessage>> {
        let pdu = match req {
            PrinterIoRequest::Create(r) => {
                // A fresh file per job: two jobs in the same second used to share (and truncate)
                // one file, and an existing file of that name was overwritten.
                let stamp = chrono::Local::now().format("%Y-%m-%d %H.%M.%S");
                let path = self.dir.join(format!("Nexus print {stamp} #{}.ps", self.next_id));
                let file = if self.jobs.len() >= MAX_OPEN_PRINT_JOBS {
                    Err(std::io::Error::other("too many print jobs open"))
                } else {
                    std::fs::OpenOptions::new().write(true).create_new(true).open(&path)
                };
                match file {
                    Ok(f) => {
                        let id = self.next_id;
                        self.next_id += 1;
                        self.jobs.insert(id, (f, path, 0));
                        RdpdrPdu::DeviceCreateResponse(DeviceCreateResponse {
                            device_io_reply: DeviceIoResponse::new(r.device_io_request, NtStatus::SUCCESS),
                            file_id: id,
                            information: Information::FILE_OPENED,
                        })
                    }
                    Err(e) => {
                        tracing::warn!(error = %e, dir = %self.dir.display(), "couldn't create a print file");
                        RdpdrPdu::DeviceCreateResponse(DeviceCreateResponse {
                            device_io_reply: DeviceIoResponse::new(r.device_io_request, NtStatus::ACCESS_DENIED),
                            file_id: 0,
                            information: Information::empty(),
                        })
                    }
                }
            }
            PrinterIoRequest::Write(r) => {
                let n = r.write_data.len() as u64;
                let ok = match self.jobs.get_mut(&r.device_io_request.file_id) {
                    Some((f, _, written)) if *written + n <= MAX_PRINT_JOB => {
                        *written += n;
                        f.write_all(&r.write_data).is_ok()
                    }
                    _ => false,
                };
                RdpdrPdu::DeviceWriteResponse(DeviceWriteResponse {
                    device_io_reply: DeviceIoResponse::new(r.device_io_request, if ok { NtStatus::SUCCESS } else { NtStatus::UNSUCCESSFUL }),
                    length: if ok { n as u32 } else { 0 },
                })
            }
            PrinterIoRequest::Close(r) => {
                if let Some((f, path, written)) = self.jobs.remove(&r.device_io_request.file_id) {
                    drop(f);
                    if written == 0 {
                        let _ = std::fs::remove_file(&path);
                    } else {
                        (self.on_printed)(path);
                    }
                }
                RdpdrPdu::DeviceCloseResponse(DeviceCloseResponse { device_io_response: DeviceIoResponse::new(r.device_io_request, NtStatus::SUCCESS) })
            }
        };
        Ok(vec![SvcMessage::from(pdu)])
    }
}

impl RdpdrBackend for Devices {
    fn handle_server_device_announce_response(&mut self, _: ServerDeviceAnnounceResponse) -> PduResult<()> {
        Ok(())
    }

    fn handle_scard_call(&mut self, _: DeviceControlRequest<ScardIoCtlCode>, _: ScardCall) -> PduResult<()> {
        Ok(())
    }

    fn handle_drive_io_request(&mut self, req: ServerDriveIoRequest) -> PduResult<Vec<SvcMessage>> {
        #[cfg(unix)]
        if let Some(d) = &mut self.drive {
            return d.handle(req);
        }
        ConfinedDrive::refuse(req)
    }

    fn handle_printer_io_request(&mut self, req: PrinterIoRequest) -> PduResult<Vec<SvcMessage>> {
        match &mut self.printer {
            Some(p) => p.handle(req),
            None => Ok(vec![SvcMessage::from(RdpdrPdu::DeviceCloseResponse(DeviceCloseResponse {
                device_io_response: DeviceIoResponse::new(req.into_device_io_request(), NtStatus::NOT_SUPPORTED),
            }))]),
        }
    }
}

#[derive(Debug)]
struct ConfinedDrive {
    #[cfg(unix)]
    inner: NixRdpdrBackend,
    #[cfg_attr(not(unix), allow(dead_code))]
    base: PathBuf,
}

impl ConfinedDrive {
    pub fn new(folder: &Path) -> std::io::Result<ConfinedDrive> {
        let base = folder.canonicalize()?;
        if !base.is_dir() {
            return Err(std::io::Error::other(format!("{} is not a folder", base.display())));
        }
        Ok(ConfinedDrive {
            #[cfg(unix)]
            inner: NixRdpdrBackend::new(base.to_string_lossy().trim_end_matches('/').to_string()),
            base,
        })
    }

    /// Whether a server path (`\dir\file`, Windows separators) stays inside the share.
    fn allowed(&self, server_path: &str) -> bool {
        let rel = server_path.replace('\\', "/");
        if rel.contains('\0') {
            return false;
        }
        let mut p = self.base.clone();
        for c in rel.split('/').filter(|c| !c.is_empty() && *c != ".") {
            if c == ".." {
                return false;
            }
            p.push(c);
        }
        // The deepest part that exists must resolve (through any symlinks) inside the share; what
        // doesn't exist yet can't be a link.
        let mut probe = p.as_path();
        loop {
            if probe.symlink_metadata().is_ok() {
                return probe.canonicalize().is_ok_and(|real| real.starts_with(&self.base));
            }
            match probe.parent() {
                Some(parent) if parent.starts_with(&self.base) => probe = parent,
                _ => return false,
            }
        }
    }

    fn refuse(req: ServerDriveIoRequest) -> PduResult<Vec<SvcMessage>> {
        tracing::warn!("RDP server asked for a path outside the shared folder; refused");
        let pdu = match req {
            ServerDriveIoRequest::ServerCreateDriveRequest(r) => RdpdrPdu::DeviceCreateResponse(DeviceCreateResponse {
                device_io_reply: DeviceIoResponse::new(r.device_io_request, NtStatus::ACCESS_DENIED),
                file_id: 0,
                information: Information::empty(),
            }),
            ServerDriveIoRequest::ServerDriveQueryDirectoryRequest(r) => RdpdrPdu::ClientDriveQueryDirectoryResponse(ClientDriveQueryDirectoryResponse {
                device_io_reply: DeviceIoResponse::new(r.device_io_request, NtStatus::NO_MORE_FILES),
                buffer: None,
            }),
            ServerDriveIoRequest::ServerDriveSetInformationRequest(r) => RdpdrPdu::ClientDriveSetInformationResponse(
                ClientDriveSetInformationResponse::new(&r, NtStatus::ACCESS_DENIED).map_err(|e| ironrdp::pdu::encode_err!(e))?,
            ),
            _ => return Ok(vec![]),
        };
        Ok(vec![SvcMessage::from(pdu)])
    }
}

impl ConfinedDrive {
    #[cfg(unix)]
    fn handle(&mut self, mut req: ServerDriveIoRequest) -> PduResult<Vec<SvcMessage>> {
        if let ServerDriveIoRequest::DeviceReadRequest(r) = &mut req {
            // A short read is valid RDPDR; the server asks again for the rest.
            r.length = r.length.min(MAX_DRIVE_READ);
        }
        let ok = match &req {
            ServerDriveIoRequest::ServerCreateDriveRequest(r) => self.allowed(&r.path),
            ServerDriveIoRequest::ServerDriveQueryDirectoryRequest(r) => r.initial_query == 0 || self.allowed(r.path.trim_end_matches('*')),
            ServerDriveIoRequest::ServerDriveSetInformationRequest(r) => match &r.set_buffer {
                FileInformationClass::Rename(info) => self.allowed(&info.file_name),
                _ => true,
            },
            // Everything else names a file by an id that a checked create handed out.
            _ => true,
        };
        if ok { self.inner.handle_drive_io_request(req) } else { Self::refuse(req) }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn print_jobs_are_saved_and_capped() {
        use ironrdp::rdpdr::pdu::efs::{
            CreateDisposition, CreateOptions, DesiredAccess, DeviceCloseRequest, DeviceCreateRequest, DeviceIoRequest, DeviceWriteRequest, FileAttributes,
            MajorFunction, SharedAccess,
        };
        let dir = std::env::temp_dir().join(format!("nexus-print-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&dir);
        std::fs::create_dir_all(&dir).unwrap();
        let printed: std::sync::Arc<parking_lot::Mutex<Vec<PathBuf>>> = Default::default();
        let p2 = printed.clone();
        let mut dev = Devices::default().with_printer(dir.clone(), std::sync::Arc::new(move |f| p2.lock().push(f)));
        let io = |file_id, major| DeviceIoRequest { device_id: 2, file_id, completion_id: 1, major_function: major, minor_function: 0.into() };
        let create = || {
            PrinterIoRequest::Create(DeviceCreateRequest {
                device_io_request: io(0, MajorFunction::Create),
                desired_access: DesiredAccess::empty(),
                allocation_size: 0,
                file_attributes: FileAttributes::empty(),
                shared_access: SharedAccess::empty(),
                create_disposition: CreateDisposition::FILE_OPEN,
                create_options: CreateOptions::empty(),
                path: String::new(),
            })
        };
        let write = |id, data: &[u8]| {
            PrinterIoRequest::Write(DeviceWriteRequest { device_io_request: io(id, MajorFunction::Write), offset: 0, write_data: data.to_vec() })
        };
        let close = |id| PrinterIoRequest::Close(DeviceCloseRequest { device_io_request: io(id, MajorFunction::Close) });
        dev.handle_printer_io_request(create()).unwrap();
        dev.handle_printer_io_request(write(1, b"%!PS-Adobe-3.0\n")).unwrap();
        dev.handle_printer_io_request(write(1, b"showpage\n")).unwrap();
        dev.handle_printer_io_request(close(1)).unwrap();
        let files = printed.lock().clone();
        assert_eq!(files.len(), 1);
        assert_eq!(std::fs::read_to_string(&files[0]).unwrap(), "%!PS-Adobe-3.0\nshowpage\n");
        // An empty job leaves no file behind; writes past the cap are refused.
        dev.handle_printer_io_request(create()).unwrap();
        dev.handle_printer_io_request(close(2)).unwrap();
        dev.handle_printer_io_request(create()).unwrap();
        dev.printer.as_mut().unwrap().jobs.get_mut(&3).unwrap().2 = MAX_PRINT_JOB;
        dev.handle_printer_io_request(write(3, b"x")).unwrap();
        assert_eq!(dev.printer.as_ref().unwrap().jobs[&3].2, MAX_PRINT_JOB);
        assert_eq!(printed.lock().len(), 1);
        dev.handle_printer_io_request(close(3)).unwrap();
        // Jobs started in the same second get their own files, and only a few may be open at once.
        for _ in 0..(MAX_OPEN_PRINT_JOBS + 3) {
            dev.handle_printer_io_request(create()).unwrap();
        }
        let jobs = &dev.printer.as_ref().unwrap().jobs;
        assert_eq!(jobs.len(), MAX_OPEN_PRINT_JOBS);
        let paths: std::collections::HashSet<_> = jobs.values().map(|j| j.1.clone()).collect();
        assert_eq!(paths.len(), MAX_OPEN_PRINT_JOBS);
        std::fs::remove_dir_all(dir).unwrap();
    }

    #[test]
    fn server_paths_stay_inside_the_share() {
        let root = std::env::temp_dir().join(format!("nexus-drive-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&root);
        let share = root.join("share");
        std::fs::create_dir_all(share.join("sub")).unwrap();
        std::fs::write(root.join("secret"), "x").unwrap();
        std::os::unix::fs::symlink(&root, share.join("escape")).unwrap();
        std::os::unix::fs::symlink(share.join("sub"), share.join("inside")).unwrap();
        let d = ConfinedDrive::new(&share).unwrap();
        for ok in ["\\", "\\sub", "\\sub\\new.txt", "\\new\\deeper\\file", "\\inside\\x", "\\.\\sub"] {
            assert!(d.allowed(ok), "{ok}");
        }
        for bad in ["\\..\\secret", "\\sub\\..\\..\\secret", "\\escape\\secret", "\\escape\\new", "\\a\0b"] {
            assert!(!d.allowed(bad), "{bad:?}");
        }
        std::fs::remove_dir_all(root).unwrap();
    }
}
