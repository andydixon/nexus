//! nexus-core: everything in Nexus Terminal that isn't drawing — storage, secrets, protocol
//! clients and the session manager. The egui app is one client of this crate.

pub mod clients;
pub mod config;
pub mod exportimport;
pub mod ircformat;
pub mod keyfiles;
pub mod known_hosts;
pub mod models;
pub mod recording;
pub mod secrets;
pub mod service;
pub mod sessions;
pub mod sshconfig;
pub mod storage;
pub mod terminalenv;
pub mod updater;
pub mod validation;

pub type Error = Box<dyn std::error::Error + Send + Sync>;
pub type Result<T> = std::result::Result<T, Error>;

/// Seeded pseudo-random input for the parsers' "hostile bytes never panic" tests. Not coverage
/// guided like cargo-fuzz, but runs on stable in every CI run.
#[cfg(test)]
pub(crate) struct Noise(u64);

#[cfg(test)]
impl Noise {
    pub fn new(seed: u64) -> Noise {
        Noise(seed | 1)
    }

    pub fn next(&mut self) -> u64 {
        // xorshift64
        self.0 ^= self.0 << 13;
        self.0 ^= self.0 >> 7;
        self.0 ^= self.0 << 17;
        self.0
    }

    pub fn below(&mut self, n: usize) -> usize {
        (self.next() % n as u64) as usize
    }

    /// Up to `max` bytes, mostly drawn from `alphabet` (the bytes a parser cares about) and
    /// otherwise uniformly random.
    pub fn bytes(&mut self, max: usize, alphabet: &[u8]) -> Vec<u8> {
        let n = self.below(max + 1);
        (0..n).map(|_| if self.below(3) > 0 { alphabet[self.below(alphabet.len())] } else { self.next() as u8 }).collect()
    }
}
