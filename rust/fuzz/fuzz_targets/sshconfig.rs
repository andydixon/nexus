// SPDX-License-Identifier: LicenseRef-Proprietary
// Copyright (C) 2026 Andy Dixon t/a Uncomplex

#![no_main]

libfuzzer_sys::fuzz_target!(|data: &[u8]| nexus_core::sshconfig::fuzz_parse(data));
