# garrison

Run multiple Claude workers in parallel, each on its own git worktree.

- Workers survive restarts.
- You can commit and push without leaving the TUI.
- Everything is managed from a single tmux session.

## Setup

Requires [uv](https://github.com/astral-sh/uv), tmux, and the Claude CLI.

```bash
uv tool install .
```

## Usage

```bash
garrison          # start or reattach
garrison reset    # wipe state, kill the session and remove all worktrees
```

## Keys

**General**

| Key | Action |
|-----|--------|
| `n` | New worker |
| `q` | Quit |

**On selected worker**

| Key     | Action |
|---------|--------|
| `enter` | Open |
| `f`     | Delete worker and worktree |
| `s`     | Commit all changes and push |
| `t`     | Open a terminal in the worktree |
| `w`     | Run `yarn run watch` in the worktree |
| `c`     | Open worktree in your current VS Code workplace |

## Contributing

It's a personal tool so feel free to fork it and make it your own.

## License

MIT
