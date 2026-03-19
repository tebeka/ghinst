complete -c ghinst -f

complete -c ghinst -l version    -d 'Print version and exit'
complete -c ghinst -l purge      -d 'Remove all but the latest installed version of owner/repo'
complete -c ghinst -l list       -d 'List installed apps'
complete -c ghinst -l force      -d 'Install even if already on the latest version'
complete -c ghinst -l dir        -d 'Base install directory' -r -a '(__fish_complete_directories)'
complete -c ghinst -l completion -d 'Print shell completion script' -r -a 'bash zsh fish'
