# Desktop

## Today: `itan app`

`itan app` starts the same local server as `itan ui` (default
`127.0.0.1:4141`, `--addr` to change), then opens it in a Chromium app-mode
window: no tabs, no URL bar, a dedicated profile under
`~/.itan/app-profile` so it is its own window rather than a tab in your
daily browser. No extra dependencies — it reuses a browser you already have.

Browser resolution order: `$ITAN_BROWSER`, then `google-chrome`,
`chromium`, `chromium-browser`, `microsoft-edge`, `brave-browser`; on macOS
the `open -na "Google Chrome" --args` launcher is tried as a last resort.
If no Chromium-family browser is found, itan prints a note and falls back
to opening the default browser, exactly like `itan ui`. Either way the
server keeps running until Ctrl-C.

### The rendering sandbox

The compose engine drives the same browser headlessly, and it keeps
Chromium's sandbox on wherever the sandbox can run. Two environments refuse
it: root (Chromium declines outright) and distributions that deny
unprivileged user namespaces — Ubuntu 23.10+ does this to any binary without
a matching AppArmor profile, which is every Chrome a CI job unpacks into a
temp directory. In both cases the browser aborts before it opens a page.

Rather than guess from the machine, itan asks the browser: it launches it
once against `about:blank` and reads the verdict, then caches it. The
sandbox comes off only where the browser itself says it cannot work, and a
failure for any other reason leaves the sandbox on so the real error stays
visible. `itan doctor` reports when the sandbox is off. `ITAN_SANDBOX=1`
forces it on, `ITAN_SANDBOX=0` forces it off.

## Later: real native packaging

The editing screen is an embedded frontend served by `internal/server` —
`server.New(session).Handler()` is a plain `http.Handler`. A real native
shell (Wails v2) is therefore a thin wrapper: embed that handler in the
Wails app, point its webview at it, add an icon, and ship a single binary
with a real dock/taskbar presence. Nothing in the UI, agent, or project
state changes — the CLI, `itan ui`, `itan app`, and a Wails shell all drive
the identical server.

That server-first architecture is why native packaging stays a roadmap
item rather than a rewrite.
