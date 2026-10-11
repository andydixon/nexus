//! nexus-core: everything in Nexus Terminal that isn't drawing — storage, secrets, protocol
//! clients and the session manager. The egui app is one client of this crate.

pub mod clients;
pub mod config;
pub mod exportimport;
pub mod ircformat;
pub mod keyfiles;
pub mod models;
pub mod recording;
pub mod secrets;
pub mod sessions;
pub mod sshconfig;
pub mod storage;
pub mod terminalenv;
pub mod updater;
pub mod validation;

pub type Error = Box<dyn std::error::Error + Send + Sync>;
pub type Result<T> = std::result::Result<T, Error>;
