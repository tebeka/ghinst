complete -c ghinst -f

complete -c ghinst -o completion -d 'Print shell completion script' -r -a 'bash zsh fish'
complete -c ghinst -o version    -d 'Print version and exit'
complete -c ghinst -o purge      -d 'Remove all but the currently used version of owner/repo'
complete -c ghinst -o list       -d 'List installed apps'
complete -c ghinst -o force      -d 'Install even if already on the latest version'
complete -c ghinst -o dir        -d 'Base install directory' -r -a '(__fish_complete_directories)'
complete -c ghinst -o max-size   -d 'Maximum asset or extracted binary size in bytes; supports kb, mb, gb suffixes' -r
complete -c ghinst -o http-timeout -d 'HTTP timeout; supports time.ParseDuration formats' -r
