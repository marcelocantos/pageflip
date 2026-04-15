# Reporting a bug

Bug reports are most useful when they include environment details and
a session log. Follow these steps to collect the information before
opening an issue.

## 1. Run the doctor report

```bash
pageflip doctor > report.md
```

This writes a markdown file containing your pageflip version, macOS
version, model cache inventory, permissions state, external tool
versions, and auth status.

## 2. Append a meetcat report (if applicable)

<!-- T21 fills this section in. Placeholder only. -->

If you were also running `meetcat` at the time of the bug:

```bash
# meetcat doctor >> report.md  # uncomment when meetcat doctor lands (T21)
```

## 3. Append the session log

If you had `--log-file` enabled when the bug occurred, tail it into
the report:

```bash
pageflip doctor --log ~/.local/share/pageflip/session.ndjson >> report.md
```

If you did not enable `--log-file`, enable it in your next session:

```bash
pageflip --log-file ~/.local/share/pageflip/session.ndjson [other flags]
```

The session log contains only numeric, enum, and hashed values —
no window titles, no OCR text, no transcripts, no file paths.

## 4. Open a GitHub issue

Open a new issue at
<https://github.com/marcelocantos/pageflip/issues/new> and paste the
contents of `report.md` into the description.

Use the **Bug report** template if available.
