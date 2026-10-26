# pageflip

## Release readiness

release_readiness: ready

Pageflip has had its first real-session successes — capture → web UI
→ specialist analysis end-to-end. Slides flow into the browser, the
RMS dedup discriminates cleanly, specialists return useful output
in the audience-mode shape the project is aiming for. The author is
running the installed version against real meetings.

Forward progress (new specialists, attendee/presenter modes, video
auto-suspend, daemonisation) is now legitimate release material;
unreleased fixes carry normal user-facing urgency.

## Homebrew

The formula in `marcelocantos/homebrew-tap` is published by
[tapper](https://github.com/marcelocantos/tapper) from the dev Mac, not by CI.
After a release's assets have uploaded (`.github/workflows/release.yml`), run
`make release-tap TAG=vX.Y.Z` (or `tapper push --version vX.Y.Z`). Config lives in `tapper.yaml`;
the tap token is read from the macOS keychain, so no Actions secret is needed.
Docker must be running.
