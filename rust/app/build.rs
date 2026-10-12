//! Embeds the icon and version info into the Windows executable.
fn main() {
    println!("cargo:rerun-if-changed=../packaging/windows/nexus.ico");
    if std::env::var("CARGO_CFG_TARGET_OS").as_deref() == Ok("windows") {
        let mut res = winresource::WindowsResource::new();
        if std::path::Path::new("../packaging/windows/nexus.ico").exists() {
            res.set_icon("../packaging/windows/nexus.ico");
        }
        res.set("ProductName", "Nexus Terminal").set("FileDescription", "Nexus Terminal");
        if let Err(e) = res.compile() {
            println!("cargo:warning=Windows icon not embedded: {e}");
        }
    }
}
