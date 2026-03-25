#compdef ghinst

_ghinst() {
    _arguments \
        '-completion[print shell completion script]:shell:(bash zsh fish)' \
        '-version[print version and exit]' \
        '-purge[remove all but the currently used version of owner/repo]' \
        '-list[list installed apps]' \
        '-force[install even if already on the latest version]' \
        '-dir[base install directory]:directory:_files -/' \
        '-max-size[maximum downloaded asset size in MiB]:size (MiB):' \
        '::owner/repo[@version]:'
}

_ghinst "$@"
