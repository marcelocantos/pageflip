# pageflip

## Release readiness

release_readiness: pre-product

Pageflip is under active development and has not yet completed a
single end-to-end session (capture → specialist analysis → useful
output for a real meeting). Until that milestone lands, the project
is **pre-product**: there are no installed users, so unreleased fix
commits do not create user-facing urgency, and `/cv` must not
recommend `/release` on their basis.

The v0.1.0 tag exists as an early publishing experiment and is not
indicative of product readiness. The next release should wait until
the frontier targets (slide event loop decoupling, reliable macOS
capture, webcam-frame suppression, OCR integration, loader/semantic
bundle, slide-grouped output) have landed and a real meeting has
been analysed successfully end-to-end.

While `release_readiness: pre-product` is set:
- `/cv` ignores unreleased-fix commits when computing the next action
- `/release` can still be invoked explicitly by the user, but is
  never auto-dispatched
- Retired targets, feature commits, and bug fixes accumulate on
  master without release pressure

Flip this flag to `release_readiness: ready` (or remove the section)
once the product has had its first real-session success and users —
even just the author — are running the installed version.
