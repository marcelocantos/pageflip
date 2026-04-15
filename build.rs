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
}
