_ghinst() {
    local cur prev words cword
    _init_completion || return

    case "$prev" in
        -dir)
            _filedir -d
            return
            ;;
        -max-size)
            return
            ;;
        -completion)
            COMPREPLY=($(compgen -W "bash zsh fish" -- "$cur"))
            return
            ;;
    esac

    if [[ "$cur" == -* ]]; then
        COMPREPLY=($(compgen -W "-completion -version -purge -list -force -dir -max-size" -- "$cur"))
        return
    fi
}

complete -F _ghinst ghinst
