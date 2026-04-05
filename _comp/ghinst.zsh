#compdef ghinst

_ghinst() {
    _arguments \
        '-completion[print shell completion script]:shell:(bash zsh fish)' \
        '-version[print version and exit]' \
        '-purge[remove all but the currently used version of owner/repo]' \
        '-list[list installed apps]' \
        '-force[install even if already on the latest version]' \
        '-dir[base install directory]:directory:_files -/' \
        '-max-size[maximum asset or extracted binary size in bytes; supports kb, mb, gb suffixes]:size:' \
        '-http-timeout[HTTP timeout; supports time.ParseDuration formats]:duration:' \
        '::owner/repo[@version]:'
}

_ghinst "$@"
