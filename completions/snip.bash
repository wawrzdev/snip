_snip_complete() {
  local cur prev command word
  cur="${COMP_WORDS[COMP_CWORD]}"
  prev="${COMP_WORDS[COMP_CWORD-1]}"
  for word in "${COMP_WORDS[@]:1}"; do
    case "$word" in list|get|new|paste|file|web|completion|version|help) command="$word"; break;; esac
  done
  if [[ "$prev" == "--description" ]]; then COMPREPLY=(); return; fi
  case "$command" in
    get) COMPREPLY=( $(compgen -W '--github --gitlab --update --all' -- "$cur") ) ;;
    new|paste) COMPREPLY=( $(compgen -W '--github --gitlab --description --public' -- "$cur") ) ;;
    file)
      if [[ "$cur" == -* ]]; then COMPREPLY=( $(compgen -W '--github --gitlab --description --public' -- "$cur") )
      else COMPREPLY=( $(compgen -f -- "$cur") ); fi ;;
    list|web) COMPREPLY=( $(compgen -W '--github --gitlab' -- "$cur") ) ;;
    completion) COMPREPLY=( $(compgen -W 'bash zsh fish' -- "$cur") ) ;;
    version|help) COMPREPLY=() ;;
    *) COMPREPLY=( $(compgen -W 'list get new paste file web completion version help --help --version --github --gitlab' -- "$cur") ) ;;
  esac
}
complete -F _snip_complete snip
