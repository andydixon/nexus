# Vendored crates

Patched copies used through `[patch.crates-io]` in `rust/Cargo.toml`. Remove each once an
IronRDP release includes the fix it carries.

| Crate | Upstream | Change |
|---|---|---|
| `picky` | 7.0.0-rc.25 (crates.io) | `=x.y.z-rc.N` RustCrypto pins relaxed to `^x.y.z-rc.N` (so the stable releases russh uses satisfy them); one unused import removed. |
| `sspi` | 0.21.3 (crates.io) | Same pin relaxation. |
| `ironrdp-session` | 0.11.0 (crates.io) | Upstream fixes not yet released: #1486 "preserve bitmap source stride" (commit `80bb81b344`, applied as-is) and the RDP6 orientation flip from #1524 (`8f1737a287`). Without them xrdp desktops render sheared and upside-down tile by tile. |

Why: IronRDP pins `picky = "=7.0.0-rc.25"`, which pins `curve25519-dalek = "=5.0.0-rc.1"` and
other release candidates; russh needs the stable `curve25519-dalek 5`, and Cargo can't build two
semver-compatible versions of one crate. With the pins relaxed both resolve to the stable
releases, and the `picky` and `sspi` unit suites (402 tests) pass against them.

The `ironrdp-session` fixes are verified by the live test in `core/src/clients/rdp.rs`
(`NEXUS_RDP_TEST="host port user password"`), run against xrdp; the published crate ships no
unit tests.
