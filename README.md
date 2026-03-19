# Exo Teams

**Your Teams data, your terminal. No admin consent, no Graph API, no IT department.**

## Overview

Exo Teams is a Go CLI that gives you programmatic access to Microsoft Teams using the same internal API the desktop app uses. Read messages, send files, check deadlines, submit assignments, all from a single binary.

> Built for developers, students, and anyone stuck behind a locked-down IT tenant who just wants to read their own messages without begging an admin for API access.

***Heads up - this is still early. Things work, things break, things get fixed. If you hit a bug, open an issue.***

## Quickstart

Requires [Go 1.23+](https://go.dev/dl/).

```bash
# Install from a tagged release
go install github.com/alxxpersonal/exo-teams/cmd/exo-teams@latest

# Or build from source
git clone https://github.com/alxxpersonal/exo-teams.git
cd exo-teams
make build
```

```bash
exo-teams auth                            # login with your microsoft account
exo-teams whoami                          # check account info and token status
exo-teams list-teams                      # list all teams and channels
exo-teams get-messages "Academic Writing"  # read channel messages
exo-teams list-chats                      # list all DMs
exo-teams get-chat "Peter"                # read a DM by name
exo-teams send "<conversation-id>" "hey"   # send a message
exo-teams send-file "<id>" --file doc.pdf  # send a file
exo-teams deadlines                        # show upcoming assignment deadlines
exo-teams unread                           # show unread conversations
```

## Why

Every existing Teams tool requires either admin consent (Graph API), enterprise licensing (E5), or an Azure app registration. Exo Teams needs none of that - your tokens, your data, no middleman.

## Features

| | |
|---|---|
| **Messaging** | Read and send messages in channels, DMs, and group chats |
| **Files** | Upload, download, and list files across SharePoint drives |
| **Assignments** | List, view status, and submit assignments (education tenants) |
| **Calendar** | View upcoming calendar events |
| **Search** | Search across messages and files |
| **Activity** | View your activity feed (mentions, replies, reactions) |
| **Deadlines** | See all pending assignment deadlines sorted by date |
| **Unread** | Show unread conversations at a glance |
| **Multi-file send** | Send multiple files in one command |
| **Auth** | Device code OAuth login, auto-refresh, tokens at `~/.exo-teams/` |

## Auth

Exo Teams uses device code OAuth flow - it opens a Microsoft login page where you enter a code. Five token scopes are acquired:

- **Skype** - messaging, activity feed, read receipts
- **ChatSvcAgg** - teams, channels, chat listing
- **Teams** - middle tier operations
- **Graph** - calendar, files, search, user profiles
- **Assignments** - education assignments (bypasses admin consent via assignments.onenote.com)

All tokens stored locally at `~/.exo-teams/` with 0600 permissions. Nothing leaves your machine. Tokens auto-refresh when expired.

## All Commands

```
auth          Login, import tokens, or refresh
whoami        Show account info and token expiry
list-teams    List all teams and channels
list-chats    List all DMs and group chats
get-messages  Read messages from a channel (--since, --replies, --all)
get-chat      Read messages from a DM or group chat
send          Send a message to any conversation
send-file     Send one or more files (--file can be repeated)
new-dm        Start a new DM with a user by name
files         List files from a team's SharePoint (--drive, --all-drives)
upload        Upload a file to a team's SharePoint
download      Download a file (--drive for non-default drives)
calendar      Show upcoming calendar events (--days)
assignments   View assignments with submission status (--classes)
submit        Submit a file to an assignment
deadlines     Show pending deadlines sorted by date
unread        Show unread conversations
activity      Show activity feed (mentions, replies, reactions)
search        Search across messages and files
mark-read     Mark a conversation as read
```

All commands support `--json` for machine-readable output.

## Contributing

PRs welcome. Run `make lint && make test` before submitting. By opening a PR you agree to license your contribution under the [MIT License](LICENSE).

## Legal

This tool uses unofficial Microsoft Teams APIs for personal use and research. Use at your own discretion.

## License

[MIT](LICENSE)
