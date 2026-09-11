complete -c snip -f
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
