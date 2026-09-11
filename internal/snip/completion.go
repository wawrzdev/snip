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
		fmt.Fprint(a.Out, `complete -W "list get new paste file web completion version --github --gitlab --update --all --description --public" snip
`)
	case "zsh":
		fmt.Fprint(a.Out, `#compdef snip
_arguments '*:argument:(list get new paste file web completion version --github --gitlab --update --all --description --public)'
`)
	case "fish":
		fmt.Fprint(a.Out, `complete -c snip -f -a 'list get new paste file web completion version'
complete -c snip -l github -d 'Use GitHub'
complete -c snip -l gitlab -d 'Use GitLab'
`)
	default:
		return errors.New("completion requires bash, zsh, or fish")
	}
	return nil
}
