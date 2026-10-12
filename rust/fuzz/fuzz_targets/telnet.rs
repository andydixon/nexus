#![no_main]

libfuzzer_sys::fuzz_target!(|data: &[u8]| nexus_core::clients::telnet::fuzz_negotiation(data));
