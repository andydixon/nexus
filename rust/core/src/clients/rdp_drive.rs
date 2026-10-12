//! Drive redirection for RDP, confined to one shared folder. IronRDP's native backend joins the
//! server's paths onto the share without checking them, so a hostile server could reach
//! `\..\..\.ssh\id_ed25519`. Every path the server sends is checked here first: no `..`
//! components, and after resolving symlinks it must stay inside the share. Unix only (the native
//! backend has no Windows implementation).

use ironrdp::core::impl_as_any;
use ironrdp::pdu::PduResult;
use ironrdp::rdpdr::RdpdrBackend;
use ironrdp::rdpdr::pdu::RdpdrPdu;
use ironrdp::rdpdr::pdu::efs::{
    ClientDriveQueryDirectoryResponse, ClientDriveSetInformationResponse, DeviceControlRequest, DeviceCreateResponse, DeviceIoResponse, FileInformationClass,
    Information, NtStatus, ServerDeviceAnnounceResponse, ServerDriveIoRequest,
};
use ironrdp::rdpdr::pdu::esc::{ScardCall, ScardIoCtlCode};
use ironrdp::svc::SvcMessage;
use ironrdp_rdpdr_native::backend::NixRdpdrBackend;
use std::path::{Path, PathBuf};

#[derive(Debug)]
pub struct ConfinedDrive {
    inner: NixRdpdrBackend,
    base: PathBuf,
}

impl_as_any!(ConfinedDrive);

impl ConfinedDrive {
    pub fn new(folder: &Path) -> std::io::Result<ConfinedDrive> {
        let base = folder.canonicalize()?;
        if !base.is_dir() {
            return Err(std::io::Error::other(format!("{} is not a folder", base.display())));
        }
        Ok(ConfinedDrive { inner: NixRdpdrBackend::new(base.to_string_lossy().trim_end_matches('/').to_string()), base })
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

impl RdpdrBackend for ConfinedDrive {
    fn handle_server_device_announce_response(&mut self, pdu: ServerDeviceAnnounceResponse) -> PduResult<()> {
        self.inner.handle_server_device_announce_response(pdu)
    }

    fn handle_scard_call(&mut self, req: DeviceControlRequest<ScardIoCtlCode>, call: ScardCall) -> PduResult<()> {
        self.inner.handle_scard_call(req, call)
    }

    fn handle_drive_io_request(&mut self, req: ServerDriveIoRequest) -> PduResult<Vec<SvcMessage>> {
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
