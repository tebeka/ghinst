# ghinst

Install binaries from GitHub releases.

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

`ghinst` fetches the release from the GitHub API, selects the asset matching your OS and architecture, downloads it, extracts the binary, and installs it to `~/.local/ghinst/owner/repo@version/`. A symlink is created in `~/.local/bin/`.

## Authentication

For private repos or to avoid API rate limits, set `GITHUB_TOKEN`:

```
export GITHUB_TOKEN=your_token_here
```

## License

MIT
