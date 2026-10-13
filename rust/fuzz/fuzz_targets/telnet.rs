// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Andy Dixon

#![no_main]

libfuzzer_sys::fuzz_target!(|data: &[u8]| nexus_core::clients::telnet::fuzz_negotiation(data));
