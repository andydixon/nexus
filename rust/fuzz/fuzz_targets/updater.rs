#![no_main]

libfuzzer_sys::fuzz_target!(|data: &[u8]| nexus_core::updater::fuzz_manifest(data));
