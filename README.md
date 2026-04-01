# tulip

Run multiple Claude Code sessions in parallel on a single repo, each isolated in its own git worktree.

- Sessions survive terminal restarts and are automatically resumed.
- Jump into any project's shell, preview live changes, or commit and push from a single command.

## Requirements

- tmux
- [Claude Code](https://claude.ai/code)

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/dgzlopes/tulip/main/install.sh | bash
```

## Usage

```bash
tulip          # open the TUI
tulip reset    # wipe all projects, sessions, and worktrees
```

## TUI keys

| Key | Action |
|-----|--------|
| `n` | New project |
| `d` | Delete selected project and worktree |
| `q` | Quit |

## Project commands

Once you have projects running, interact with them from any terminal:

```bash
tulip <project>              # interactive picker (arrow keys)
tulip claude <project>       # attach to the Claude session
tulip shell <project>        # open a shell in the worktree
tulip graft <project>        # yarn install + watch (for Graft live preview)
tulip vscode <project>       # open the worktree in VS Code
tulip publish <project> <msg># stage all, commit (signed), and push
```

## Contributing

Personal tool — fork and adapt freely.

## License

MIT
