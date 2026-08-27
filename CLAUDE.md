# mdfmt

## README screenshots

`make screenshots` regenerates `docs/file.png` (rendered document view of
`docs/test.md`) and `docs/docs.png` (directory browser at the repo root).
It builds the binary, serves the repository root on port 8642 with
`--path-token none` and a throwaway HOME, and captures both pages with
`playwright-cli` (see `docs/take-screenshots.sh`). Retake the screenshots
whenever the page layout or styling changes visibly.

Inside the Claude Code sandbox a browser cannot be launched (Chrome dies at
spawn on a seatbelt denial). Attach to a running browser over CDP instead:

1. Probe: `curl -s http://localhost:9222/json/version`. If nothing answers,
   ask the user to start a headless Chrome with
   `--remote-debugging-port=9222` (for example `make chrome-for-testing` in
   the WebCaptureChromeExtension project).
2. Ensure `.playwright/cli.config.json` (gitignored) contains:
   `{"browser": {"cdpEndpoint": "http://localhost:9222"}}`
3. Run `make screenshots`. In attach mode the script closes only the tab it
   opened — never close or kill the shared browser; the user has to restart
   it manually.

Outside the sandbox no setup is needed: without the config file,
playwright-cli launches and closes its own headless browser.

`mdfmt serve` writes its port registry beneath `$HOME/.mdfmt`; the script
points HOME at a temp directory, so the real registry and the sandbox write
limits are not an issue.
