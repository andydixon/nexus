// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Andy Dixon

//! Accessors and actions specific to one protocol (RAW, IRC, GitHub, desktops, SSH).

use super::*;

impl Manager {
    // --- RAW ----------------------------------------------------------------------------

    pub(super) fn raw(&self, id: &str) -> Result<Arc<raw::RawClient>> {
        match self.client(id)? {
            Client::Raw(c) => Ok(c),
            _ => err(format!("sessions: session {id:?} is not a RAW connection")),
        }
    }

    pub fn raw_write_text(&self, id: &str, s: &str) -> Result<()> {
        self.raw(id)?.write_text(s)
    }

    pub fn raw_write_hex(&self, id: &str, s: &str) -> Result<()> {
        self.raw(id)?.write_hex(s)
    }

    pub fn raw_write_escaped(&self, id: &str, s: &str) -> Result<()> {
        self.raw(id)?.write_escaped(s)
    }

    pub fn raw_history(&self, id: &str) -> Result<Vec<raw::HistoryEntry>> {
        Ok(self.raw(id)?.history())
    }

    pub fn raw_clear_history(&self, id: &str) -> Result<()> {
        self.raw(id)?.clear_history();
        Ok(())
    }

    pub fn raw_save_capture(&self, id: &str, path: &std::path::Path) -> Result<()> {
        self.raw(id)?.save_capture(path)
    }

    // --- IRC ----------------------------------------------------------------------------

    pub fn irc(&self, id: &str) -> Result<Arc<irc::Client>> {
        match self.client(id)? {
            Client::Irc(c, _) => Ok(c),
            _ => err(format!("sessions: session {id:?} is not an IRC connection")),
        }
    }

    // --- GitHub -------------------------------------------------------------------------

    pub fn github(&self, id: &str) -> Result<Arc<github::Client>> {
        match self.client(id)? {
            Client::Github(c, _) => Ok(c),
            _ => err(format!("sessions: session {id:?} is not a GitHub connection")),
        }
    }

    pub fn rdp(&self, id: &str) -> Result<Arc<clients::rdp::Client>> {
        match self.client(id)? {
            Client::Rdp(c, _) => Ok(c),
            _ => err(format!("sessions: session {id:?} is not an RDP connection")),
        }
    }

    pub fn vnc(&self, id: &str) -> Result<Arc<clients::vnc::Client>> {
        match self.client(id)? {
            Client::Vnc(c, _) => Ok(c),
            _ => err(format!("sessions: session {id:?} is not a VNC connection")),
        }
    }

    // --- SSH extras ---------------------------------------------------------------------

    pub fn ssh(&self, id: &str) -> Result<Arc<SshClient>> {
        match self.client(id)? {
            Client::Ssh(c) => Ok(c),
            _ => err(format!("sessions: session {id:?} is not an SSH connection")),
        }
    }

    pub fn ping(&self, id: &str) -> Result<i64> {
        self.ssh(id)?.ping()
    }

    pub fn cwd(&self, id: &str) -> Result<String> {
        self.ssh(id)?.run_command("pwd")
    }

    pub fn start_forward(&self, id: &str, rule: PortForward) -> Result<()> {
        self.ssh(id)?.start_forward(rule.clone())?;
        self.emit(Event::ForwardStarted { session: id.into(), forward: rule });
        Ok(())
    }

    pub fn stop_forward(&self, id: &str, fid: &str) -> Result<()> {
        self.ssh(id)?.stop_forward(fid)?;
        self.emit(Event::ForwardStopped { session: id.into(), forward_id: fid.into() });
        Ok(())
    }

    pub fn forwards(&self, id: &str) -> Result<Vec<PortForward>> {
        Ok(self.ssh(id)?.active_forwards())
    }

    pub fn sftp_download(&self, id: &str, remote: &str, local: &std::path::Path) -> Result<()> {
        let s = self.ssh(id)?.sftp()?;
        clients::sftp::download(&s, remote, local, &|p| self.emit(Event::SftpProgress { session: id.into(), progress: p }))
    }

    pub fn sftp_upload(&self, id: &str, local: &std::path::Path, remote: &str) -> Result<()> {
        let s = self.ssh(id)?.sftp()?;
        clients::sftp::upload(&s, local, remote, &|p| self.emit(Event::SftpProgress { session: id.into(), progress: p }))
    }
}
