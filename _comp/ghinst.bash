_ghinst() {
    local cur prev words cword
    _init_completion || return

    case "$prev" in
        -dir)
            _filedir -d
            return
            ;;
        -completion)
            COMPREPLY=($(compgen -W "bash zsh fish" -- "$cur"))
            return
            ;;
    esac

    if [[ "$cur" == -* ]]; then
        COMPREPLY=($(compgen -W "-version -purge -list -force -dir -completion" -- "$cur"))
        return
    fi
}

complete -F _ghinst ghinst
