package snip

import (
	"errors"
	"fmt"
)

func (a *App) completion(args []string) error {
	if len(args) != 1 {
		return errors.New("completion requires bash, zsh, or fish")
	}
	switch args[0] {
	case "bash":
		fmt.Fprint(a.Out, `_snip_complete() {
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
`)
	case "zsh":
		fmt.Fprint(a.Out, `#compdef snip
_snip() {
  local command word command_index i
  for (( i = 2; i <= ${#words}; i++ )); do
    word="${words[i]}"
    case "$word" in
      list|get|new|paste|file|web|completion|version|help)
        command="$word"; command_index=$i; break;;
    esac
  done
  if [[ -n "$command_index" ]]; then
    words=("${(@)words[1,$((command_index-1))]}" "${(@)words[$((command_index+1)),-1]}")
    (( CURRENT > command_index )) && (( CURRENT-- ))
  fi
  case "$command" in
    get) _arguments '--github[use GitHub]' '--gitlab[use GitLab]' '--update[update existing clones]' '--all[operate on all items]' '*:name, URL, or ID:' ;;
    new|paste) _arguments '--github[use GitHub]' '--gitlab[use GitLab]' '--description[set description]:description:' '--public[make public]' '1:filename:' ;;
    file) _arguments '--github[use GitHub]' '--gitlab[use GitLab]' '--description[set description]:description:' '--public[make public]' '1:file:_files' ;;
    list) _arguments '--github[use GitHub]' '--gitlab[use GitLab]' ;;
    web) _arguments '--github[use GitHub]' '--gitlab[use GitLab]' '*:query:' ;;
    completion) _values shell bash zsh fish ;;
    version|help) _message 'no more arguments' ;;
    *) _arguments '--help[show help]' '--version[show version]' '--github[use GitHub]' '--gitlab[use GitLab]' '1:command:(list get new paste file web completion version help)' '*:query:' ;;
  esac
}
_snip "$@"
`)
	case "fish":
		fmt.Fprint(a.Out, `complete -c snip -f
complete -c snip -n 'not __fish_seen_subcommand_from list get new paste file web completion version help' -a 'list get new paste file web completion version help'
complete -c snip -n 'not __fish_seen_subcommand_from list get new paste file web completion version help' -l help -d 'Show help'
complete -c snip -n 'not __fish_seen_subcommand_from list get new paste file web completion version help' -l version -d 'Show version'
complete -c snip -n 'not __fish_seen_subcommand_from completion version help' -l github -d 'Use GitHub'
complete -c snip -n 'not __fish_seen_subcommand_from completion version help' -l gitlab -d 'Use GitLab'
complete -c snip -n '__fish_seen_subcommand_from get' -l update -d 'Update existing clones'
complete -c snip -n '__fish_seen_subcommand_from get' -l all -d 'Operate on all items'
complete -c snip -n '__fish_seen_subcommand_from new paste file' -l description -r -d 'Set description'
complete -c snip -n '__fish_seen_subcommand_from new paste file' -l public -d 'Make public'
complete -c snip -n '__fish_seen_subcommand_from file' -F
complete -c snip -n '__fish_seen_subcommand_from completion' -a 'bash zsh fish'
`)
	default:
		return errors.New("completion requires bash, zsh, or fish")
	}
	return nil
}
