#compdef ghinst

_ghinst() {
    _arguments \
        '-version[print version and exit]' \
        '-purge[remove all but the latest installed version of owner/repo]' \
        '-list[list installed apps]' \
        '-force[install even if already on the latest version]' \
        '-dir[base install directory]:directory:_files -/' \
        '-completion[print shell completion script]:shell:(bash zsh fish)' \
        '::owner/repo[@version]:'
}

_ghinst "$@"
