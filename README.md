
<img width="81" height="134" alt="tulip" src="https://github.com/user-attachments/assets/514b0852-fb82-49b5-a352-3050d060d72e" /> 

# tulip

> ⚠️ This tool is purposely built for myself and how I develop things at Grafana Labs (e.g. using Graft)

Run multiple Claude Code sessions in parallel on a single repo, each isolated in its own git worktree.

- Sessions survive terminal restarts and are automatically resumed.
- Jump into any project's shell, preview live changes with Graft, or commit and push from a single command.
- Fully isolated from your personal tmux — tulip runs its own tmux server.

https://github.com/user-attachments/assets/c750da08-e3db-473f-b013-555b248aa3fd

## Requirements

- tmux, or a [Rex](https://www.superlogical.com/rex) terminal
- [Claude Code](https://claude.ai/code)

## The project list

The list grows to fill the terminal. Projects whose PR has merged are hidden by default (nothing is deleted); press `m` to show or hide them. Git status and graft state refresh in the background, so large repos with many projects stay responsive.

## Backends

tulip runs each session in either tmux or Rex.

- **tmux** (default): sessions live in a dedicated tmux server; actions attach to it.
- **Rex**: used automatically when tulip runs inside a Rex terminal (`REX_SESSION` is set). The TUI stays open in its own tab, and each action opens or focuses a tab in the same Rex session: `<branch> · claude`, `<branch> · shell`, `<branch> · watch`. A running graft shows in the Claude tab's label (`graft: active` or `graft: failed`).

Force one with `--backend=rex|tmux` or `TULIP_BACKEND=rex|tmux`.

Tab names can take an optional per-repo prefix: `tulip config tab-prefix "Synthetics: App · "` names the Claude tab `Synthetics: App · <branch>` and the others `Synthetics: App · <branch> · shell`. The prefix is used as-is, so include your own separator and trailing space. Run `tulip config tab-prefix` with no text to clear it. With a prefix set, new tabs are also placed right after the last existing tab with the same prefix, so a repo's tabs stay together. Without a prefix, tabs are named `<branch> · claude`, `<branch> · shell`, and so on.

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
| `↑↓` / `j` `k` | Navigate |
| `↵` | Open project action menu |
| `?` | Help |
| `q` | Quit |

### Status indicators

| Dot | Meaning |
|-----|---------|
| grey | No uncommitted changes |
| green | Uncommitted changes in worktree |
| red | Worktree error (e.g. missing path) |

| Icon | Meaning |
|------|---------|
| 🐝 (green) | Yarn watch is running |
| 🐝 failed (red) | Watch exited unexpectedly |

## Project commands

Interact with projects by ID or branch name from any terminal:

```bash
tulip <id|branch>                # interactive picker (arrow keys)
tulip claude <id|branch>         # attach to the Claude session
tulip shell <id|branch>          # open a shell in the worktree
tulip graft <id|branch>          # start yarn watch for live preview (switches active graft)
tulip graft-debug <id|branch>    # attach to the graft watch output
tulip graft-urls                    # list the Grafana Cloud pages Graft is configured to preview on
tulip vscode <id|branch>            # open the worktree in VS Code
tulip publish <id|branch> <msg>     # stage all, commit (signed), and push
tulip config graft-command <cmd>    # set the command run when grafting (default: yarn install && yarn run watch)
tulip config graft-domain [domain]  # set the default Graft domain (opens a picker if domain is omitted)
```

The `↵` action menu also provides: **difit** (visual diff, requires [difit](https://github.com/yoshiko-pg/difit)) and **open-pr** (open PR in browser).

Every `claude` session tulip starts is launched with `--chrome`, so it can use Claude in Chrome to look at whatever Grafana Cloud page you have open — including a live Graft preview of that session's own branch. `tulip graft-urls` reads Graft's own global config (`~/.config/graft`) and shows every domain it's currently set up to intercept, plus its interception/plugin state; `tulip graft <id|branch>` prints the same list right after switching the preview. If more than one domain is enabled, `tulip config graft-domain` marks (and lets you change) which one is treated as *the* preview URL for this repo — shown first in `graft-urls` and used for the notification when grafting finishes.

## License

MIT
