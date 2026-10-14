# Building from source

Only needed if you want to change the code. Users should
[download a release](https://github.com/Myname124/multi-claude-switcher/releases/latest)
instead: the shipped app updates itself.

Requires Go 1.22 or newer.

## Which binary is which

The project runs on Windows only. It builds two binaries:

| Binary | What it is |
|---|---|
| `cmd/mcs-tray` | **The shipped app.** System-tray icon plus a WebView2 panel; pure Go, no CGO. |
| `cmd/mcs` | Command-line interface. Not shipped in releases; see [cli.md](cli.md). |

## The app

Pure Go, no CGO and no C toolchain:

```powershell
$env:CGO_ENABLED = '0'
go build -ldflags '-H=windowsgui' -o bin/mcs-tray.exe ./cmd/mcs-tray
```

`-H=windowsgui` makes it a GUI-subsystem executable, so it runs from the tray
with no console window.

To build the installer you also need [Inno Setup](https://jrsoftware.org/isinfo.php):

```powershell
& "C:\Program Files (x86)\Inno Setup 6\ISCC.exe" /DMyAppVersion=0.10.1 packaging\windows-setup.iss
```

## CLI

```powershell
go build -o bin/mcs.exe ./cmd/mcs
```

## Tests

```powershell
go test ./...
```

The packages only build for Windows, so run the tests there (CI does, on every
push).

On Windows the repository is checked out with CRLF line endings, so `gofmt -l`
flags every file. Check formatting on LF-normalised copies rather than in place.

## Releasing

Pushing a `v*` tag triggers `.github/workflows/release.yml`, which runs the
tests, builds the Windows installer and publishes it as a GitHub Release.
Nothing is released without a tag.

## Regenerating the README screenshot

`docs/assets/panel.png` is rendered from the shipped panel code with placeholder
accounts, not photographed, so it cannot drift from the real UI and never
contains anyone's real account names. After a UI change:

```bash
go run ./scripts/gen-screenshot panel.html
msedge --headless --disable-gpu --hide-scrollbars \
  --force-device-scale-factor=2 --screenshot=docs/assets/panel.png \
  --window-size=400,545 "file://$PWD/panel.html"
```

Any Chromium works in place of `msedge`.
