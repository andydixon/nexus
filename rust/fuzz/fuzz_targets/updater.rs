// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Andy Dixon

#![no_main]

libfuzzer_sys::fuzz_target!(|data: &[u8]| nexus_core::updater::fuzz_manifest(data));
