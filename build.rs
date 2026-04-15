// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

//! Add the macOS Swift runtime rpath so ScreenCaptureKit's dependency on
//! `libswift_Concurrency.dylib` resolves at runtime.
//!
//! `screencapturekit` v1.5.x sets this rpath via its own build script, but
//! `cargo:rustc-link-arg` only applies to the crate that emits it, not
//! transitively to downstream binaries. We have to repeat the directive in
//! pageflip's own build.rs so the rpath lands on the final executable.

fn main() {
    if std::env::var("CARGO_CFG_TARGET_OS").as_deref() == Ok("macos") {
        println!("cargo:rustc-link-arg=-Wl,-rpath,/usr/lib/swift");
    }

    // Embed the git SHA so `pageflip doctor` can report it.
    let sha = std::process::Command::new("git")
        .args(["rev-parse", "--short", "HEAD"])
        .output()
        .ok()
        .and_then(|o| {
            if o.status.success() {
                String::from_utf8(o.stdout)
                    .ok()
                    .map(|s| s.trim().to_string())
            } else {
                None
            }
        })
        .unwrap_or_else(|| "unknown".to_string());
    println!("cargo:rustc-env=PAGEFLIP_GIT_SHA={sha}");
    println!("cargo:rerun-if-changed=.git/HEAD");
}
