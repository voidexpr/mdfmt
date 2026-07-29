# mdfmt

`mdfmt` turns Markdown into a polished local documentation site or a
self-contained HTML file. It is a single Go binary with embedded templates,
styles, JavaScript, and syntax-highlighting definitions, so it works offline
without adjacent runtime assets.

## Install

`mdfmt` requires Go 1.26.5 or newer.

```sh
go install github.com/nicad/mdfmt@latest
```

To build a checkout:

```sh
make build
./mdfmt --help
```

## Commands

```text
mdfmt serve [OPTIONS] [PATH]
mdfmt save [OPTIONS] SOURCE
```

Options may appear before or after the positional path. Run `mdfmt --help`,
`mdfmt serve --help`, or `mdfmt save --help` for the built-in command
reference.

### Serve a Markdown tree

```sh
mdfmt serve --bind 127.0.0.1 --port 8642 ./docs
```

`serve` presents an existing directory tree of `.md` and `.markdown` files
(case-insensitively) as a local documentation site. `PATH` defaults to the
current directory, and port `0` asks the operating system to select an
available port.

| Option | Default | Behavior |
| --- | --- | --- |
| `--bind IP` | `127.0.0.1` | Bind to the given IP address. Hostnames are not accepted. |
| `--port NUM` | `0` | Bind to the given TCP port. |
| `--edit-command COMMAND` | disabled | Add Edit controls that directly launch `COMMAND`. |
| `--edit-arg VALUE` | none | Insert an argument before the file path in the editor command. Repeat for multiple arguments. |
| `--edit-sublime` | disabled | Add Edit controls using the Sublime Text `subl` CLI. |
| `--edit-vscode` | disabled | Add Edit controls using the Visual Studio Code `code` CLI. |

The resolved root is the server's filesystem boundary. Directory pages and
Markdown documents are generated when requested, so refreshing shows edits
without restarting the server. Non-Markdown files, dotfiles, and
dot-directories are not exposed. Symlinks are followed only when their fully
resolved targets remain beneath the root.

Directory navigation, breadcrumbs, heading IDs, a table of contents, sorting,
responsive light/dark styling, and static assets are self-contained in the
binary. Unsafe raw HTML in Markdown is omitted. Every document page also has a
Raw link that serves the exact source bytes as plain text.

For quick local edits while browsing:

```sh
mdfmt serve ./docs --edit-sublime
mdfmt serve ./docs --edit-vscode
mdfmt serve ./docs --edit-command /usr/local/bin/my-editor
```

The editor selectors are mutually exclusive. `--edit-arg` requires one of
them and can be repeated. Editor integration is disabled by default and may
only be used with a loopback bind address. The selected executable must resolve
at startup. Accepted edit requests invoke it directly without a shell, after
the requested Markdown path is resolved and checked against the server root.

The server reports its resolved root and address at startup, shuts down
gracefully on SIGINT or SIGTERM, and does not write to the served tree.

### Save standalone HTML

```sh
mdfmt save ./docs/guide.md
mdfmt save ./docs/guide.md --output ./preview.html
mdfmt save ./docs/guide.md --output ./public --hash --quiet
```

`save` renders one regular source file as a self-contained HTML document with
the same Markdown rendering, heading IDs, table of contents, theme, metadata,
and safe raw-HTML policy as `serve`. The source filename extension is ignored.

| Option | Default | Behavior |
| --- | --- | --- |
| `-o`, `--output TARGET` | system temporary directory | Write to an explicit filename or, when `TARGET` is an existing directory, generate a filename inside it. Parent directories are never created. |
| `--hash` | disabled | Use a stable 24-hex-digit filename derived from the canonical source path. Valid only for generated filenames. |
| `-q`, `--quiet` | disabled | Do not print the absolute output path after a successful save. |

Without `--output`, the result is atomically written beneath
`$TMPDIR/mdfmt/` using the source basename with an `.html` extension. An
existing output directory uses the same generated name. A non-directory
`TARGET`, whether it exists or not, is treated as the exact output filename.
Existing output files are atomically replaced, but the source itself can never
be the target.

The saved file embeds its CSS and JavaScript, contains no source directory
path, starts no server, and needs no adjacent assets.

## Syntax highlighting

Fenced code blocks are highlighted from their language label using Chroma's
built-in language set, plus the project-specific `qeylan` and `qy` labels.
Unknown and unlabeled fences remain escaped plain text. `serve` delivers one
shared light/dark syntax stylesheet; `save` embeds the same stylesheet.

After changing the highlighter or its styles, regenerate and verify the checked
in CSS:

```sh
go generate ./internal/mdhighlight
git diff -- assets/syntax.css
```

## Development

```sh
make test       # unit and integration tests
make test-race  # race detector
make vet        # go vet
make coverage   # coverage report
make ci         # full local CI suite
```

`make ci` additionally runs staticcheck, checks generated syntax CSS and module
tidiness, verifies formatting, and runs `govulncheck`. Run `make install` once
to install the pinned staticcheck and govulncheck versions it uses.
