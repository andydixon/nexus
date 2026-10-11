#!/usr/bin/env bash
# Release builds live with the Rust workspace; see docs/build-and-release.md.
exec "$(dirname "${BASH_SOURCE[0]}")/rust/release.sh" "$@"
