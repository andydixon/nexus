#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Copyright (C) 2026 Andy Dixon
# Release builds live with the Rust workspace; see docs/build-and-release.md.
exec "$(dirname "${BASH_SOURCE[0]}")/rust/release.sh" "$@"
