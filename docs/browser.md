# Shared headless browser

```sh
baton browser start
baton browser status
baton browser endpoint
baton browser stop
```

`start` prints a `ws://127.0.0.1:PORT/devtools/browser/ID` endpoint. Repeated
or concurrent starts reuse one browser per baton state directory. `endpoint`
fails if none is running. `status` reports the browser PID and open CDP
WebSocket connections. One tool may open several connections.

The browser stays warm for ten minutes after its last client disconnects.
Open connections prevent idle shutdown. HTTP discovery and status polling do
not keep it alive. `stop` disconnects every client. Browser data is temporary;
close your own pages and contexts when a job finishes.

Commands wait through a missing session file while the supervisor holds its
lifetime lock, up to the 15-second command deadline. After shutdown, `status`
reports stopped and `endpoint` fails because no browser is running. Corrupt
session metadata and other errors fail immediately.

## Configuration

Add settings to the file printed by `baton config path`:

```toml
[browser]
# Empty selects an installed browser automatically.
executable = ""
flags = ["--window-size=1440,900"]
idle_timeout = "10m"
```

Discovery checks PATH, standard Chrome/Chromium/Edge locations, then
Playwright's cache. `PLAYWRIGHT_BROWSERS_PATH` selects another cache directory.
With Playwright's package-local setting (`0`), set `browser.executable` to
that installation's executable. No browser is downloaded.

baton supplies `--headless=new`, `--enable-gpu`, a new private profile, and an
ephemeral loopback debugging port. Extra flags use `--name` or `--name=value`.
Use `--disable-gpu` when required by your environment; GPU acceleration
still depends on the browser, drivers, and host. Headless mode, profile,
and remote-debugging flags are reserved. Container users must provide Chrome
and its native dependencies; baton does not disable Chrome's sandbox.

Configuration changes apply on the next fresh start. Startup failures point
to `$XDG_STATE_HOME/baton/browser/browser.log` (default
`~/.local/state/baton/browser/browser.log`). Each start replaces that log.

## Playwright

Export an endpoint for your own script to read:

```sh
export BATON_BROWSER_ENDPOINT="$(baton browser start)"
node render.mjs
```

`render.mjs`, using an already installed Playwright:

```js
import { chromium } from 'playwright';

const browser = await chromium.connectOverCDP(process.env.BATON_BROWSER_ENDPOINT);
const context = await browser.newContext();
try {
  const page = await context.newPage();
  await page.setContent('<h1>baton</h1>');
  await page.screenshot({ path: 'baton.png' });
} finally {
  await context.close();
  await browser.close();
}
```

[Playwright documents CDP connections](https://playwright.dev/docs/api/class-browsertype#browser-type-connect-over-cdp)
as Chromium-only and lower fidelity than its native protocol.
[Closing a connected browser](https://playwright.dev/docs/api/class-browser#browser-close)
disconnects this client.

## Puppeteer

Use the same exported variable in your script:

```js
import puppeteer from 'puppeteer';

const browser = await puppeteer.connect({
  browserWSEndpoint: process.env.BATON_BROWSER_ENDPOINT,
});
try {
  const page = await browser.newPage();
  try {
    await page.setContent('<h1>baton</h1>');
    await page.screenshot({ path: 'baton.png' });
  } finally {
    await page.close();
  }
} finally {
  await browser.disconnect();
}
```

[Puppeteer accepts `browserWSEndpoint`](https://pptr.dev/api/puppeteer.connectoptions).
Use [`disconnect()`](https://pptr.dev/api/puppeteer.browser.disconnect) to
leave the shared browser running. These scripts read
`BATON_BROWSER_ENDPOINT` explicitly; baton does not inject it into tools.

## Chrome DevTools MCP

For an already installed `chrome-devtools-mcp`, derive its HTTP discovery
address from baton's WebSocket endpoint:

```sh
export BATON_BROWSER_ENDPOINT="$(baton browser start)"
export BATON_BROWSER_URL="$(node -e 'const u = new URL(process.env.BATON_BROWSER_ENDPOINT); console.log(`http://${u.host}`)')"
CHROME_DEVTOOLS_MCP_NO_UPDATE_CHECKS=1 chrome-devtools-mcp \
  --browserUrl "$BATON_BROWSER_URL" --no-usage-statistics --no-performance-crux
```

[The MCP configuration reference](https://github.com/ChromeDevTools/chrome-devtools-mcp/blob/main/docs/configuration.md)
documents `--browserUrl`; the HTTP discovery response points back through
baton's counting proxy. A wrapper that accepts an endpoint through an
environment variable can read `BATON_BROWSER_ENDPOINT` or `BATON_BROWSER_URL`
and pass it to its own connection option. Setting either variable alone
does not make arbitrary renderers reuse this browser.

## Lifetime and limits

Only `browser start` launches a background supervisor. `run` and `status`
keep their existing daemon-free operation. Control requests require a
private session token; CDP is available to local processes. Nonlocal Host
headers and requests with an Origin header are rejected. The private Chrome
port must not be used directly: those connections bypass client counting.

On macOS/Linux, a subsequent `start` or `stop` recovers a recorded orphan
left by a killed supervisor. Recovery checks process birth identity, user,
and the exact baton profile argument before signalling. Corrupt or changed
ownership fails explicitly. Profiles left by a supervisor crash remain;
normal shutdown removes its own profile. A crash between process creation
and ownership publication can leave an unrecorded orphan for `baton reap`.
Windows compiles, but runtime and supervisor-crash recovery are unverified;
its driver never signals a persisted PID.

Verified here: the native CLI exchanged `Browser.getVersion` with an owned
Chrome 156 process through the proxy. Native/Linux process tests cover
concurrent starts, counted connections, idle expiry, startup failure, and
supervisor-crash recovery. The JavaScript/MCP recipes have primary-source
API checks; their full tool flows have not been executed in this iteration.
