# ghinst

<p align="center"><img src="logo.png" width="400" alt="ghinst logo"/></p>

Install binaries from GitHub releases to `~/.local/bin`.

## Install

```
go install github.com/tebeka/ghinst@latest
```

## Usage

```
ghinst owner/repo[@version]
```

Install the latest release:
```
ghinst junegunn/fzf
```

Install a specific version:
```
ghinst junegunn/fzf@v0.54.0
```

## How It Works

`ghinst` fetches the release from the GitHub API, selects the asset matching your OS and architecture, downloads it, verifies the GitHub-provided checksum when available, extracts the binary, and installs it to `~/.local/ghinst/owner/repo@version/`. A symlink is created in `~/.local/bin/`. If GitHub does not provide a checksum for the asset, `ghinst` prints a warning and continues.

You can change the installation directory location by setting the `GHINST_DIR` environment variable.

By default, downloads are limited to `200 MiB`, and extracted binaries are limited to `100 MiB`. Use `-max-size` to lower or raise the download limit:


```
ghinst -max-size 300 owner/repo
```

## Authentication

For private repos or to avoid API rate limits, set `GITHUB_TOKEN`:

```
export GITHUB_TOKEN=your_token_here
```

## License

MIT
