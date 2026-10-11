//! nexus-core: everything in Nexus Terminal that isn't drawing — storage, secrets, protocol
//! clients and the session manager. The egui app is one client of this crate.

pub mod clients;
pub mod config;
pub mod exportimport;
pub mod ircformat;
pub mod models;
pub mod secrets;
pub mod storage;

pub type Error = Box<dyn std::error::Error + Send + Sync>;
pub type Result<T> = std::result::Result<T, Error>;
