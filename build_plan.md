# Missing Seat Role Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Create a `/set_missing_seat_role` command and logic to automatically assign a missing seat role and send a greeting message to users missing a required SeAT login, removing the role once they comply.

**Architecture:** We will store the `missing_seat_role` ID in the `config` table similar to `greeting_channel`. In the roles synchronization loops (`roles.go` and `guest_roles.go`), we will track if any mapped corp/alliance role was denied because of `!isSeatActive`. If denied, we assign the `missing_seat_role` (if not already present) and send a targeted message in the greeting channel. If they have the role but no longer need it (because they met requirements or were removed), we remove it.

**Tech Stack:** Go, DiscordGo, SQLite.

---

### Task 1: Create the `/set_missing_seat_role` Command

**Files:**
- Modify: `commands/commands.go`
- Modify: `commands/commands_test.go`

**Interfaces:**
- Consumes: The `db.DB` connection for writing to `config` table.
- Produces: Updates `config` table with `key = 'missing_seat_role'`.

- [x] **Step 1: Add command definition**
In `commands/commands.go`, add the slash command to the `Commands` array:

```go
		{
			Name:        "set_missing_seat_role",
			Description: "Set a role to assign to users who need to log into SeAT but haven't (omit to disable)",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionRole,
					Name:        "role",
					Description: "The Discord role to assign (omit to disable)",
					Required:    false,
				},
			},
		},
```

- [x] **Step 2: Add help text**
In `commands/commands.go`'s `helpCommandHandler`, add to the help string:
```go
"\\n`/set_missing_seat_role [role]` - Set a role to assign to users who need to log into SeAT but haven't (omit to disable).\\n"
```

- [x] **Step 3: Handle the command**
In `commands/commands.go` inside the `switch i.ApplicationCommandData().Name`, add the `set_missing_seat_role` case:

```go
	case "set_missing_seat_role":
		opt := i.ApplicationCommandData().Options
		var roleID string
		if len(opt) > 0 {
			roleID = opt[0].RoleValue(s, i.GuildID).ID
		}

		if roleID == "" {
			_, err := db.DB.Exec("DELETE FROM config WHERE guild_id = ? AND key = 'missing_seat_role'", guildID)
			if err != nil {
				sendResponse(s, i.Interaction, "Error disabling missing SeAT role.")
				return
			}
			sendResponse(s, i.Interaction, "Missing SeAT role has been disabled.")
			return
		}

		_, err := db.DB.Exec("INSERT OR REPLACE INTO config (guild_id, key, value) VALUES (?, 'missing_seat_role', ?)", guildID, roleID)
		if err != nil {
			sendResponse(s, i.Interaction, "Error configuring missing SeAT role.")
			return
		}

		sendResponse(s, i.Interaction, fmt.Sprintf("Missing SeAT role configured to <@&%s>", roleID))
```

- [x] **Step 4: Update tests**
In `commands/commands_test.go`, add `"set_missing_seat_role": true,` to `expectedCommands`.

- [x] **Step 5: Run tests**
Run: `go test ./commands/... -v`
Expected: PASS

- [x] **Step 6: Commit**
```bash
git add commands/commands.go commands/commands_test.go
git commit -m "feat: add /set_missing_seat_role command"
```

### Task 2: Apply Missing Seat Role Logic to Main Characters

**Files:**
- Modify: `roles/roles.go`

**Interfaces:**
- Consumes: The `missing_seat_role` from `config` table, and `integ.SeatURL`.

- [x] **Step 1: Fetch config values**
In `roles/roles.go` inside `SyncGuild` around line 515 (where `greetingChannel` is fetched):
```go
		var missingSeatRoleID string
		_ = db.DB.QueryRow("SELECT value FROM config WHERE guild_id = ? AND key = 'missing_seat_role'", guildID).Scan(&missingSeatRoleID)
```

- [x] **Step 2: Add logic to tracking requirement**
In `roles/roles.go` inside the main `for discordID, charInfo := range discordToMainChar` loop:
At the start of the loop (around line 592), define:
```go
			missingSeatRoleNeeded := false
```

When evaluating `corpToRole` around line 694, replace:
```go
					if seatNeededRoles[roleID] && !isSeatActive {
```
with:
```go
					if seatNeededRoles[roleID] && !isSeatActive {
						missingSeatRoleNeeded = true
```

When evaluating `allianceToRole` around line 730, replace:
```go
					if seatNeededRoles[roleID] && !isSeatActive {
```
with:
```go
					if seatNeededRoles[roleID] && !isSeatActive {
						missingSeatRoleNeeded = true
```

- [x] **Step 3: Apply or remove the role per user**
At the end of the user iteration in the `for discordID, charInfo := range discordToMainChar` loop (after standing roles), add:
```go
			if missingSeatRoleID != "" {
				hasMissingRole := HasRole(member.Roles, missingSeatRoleID)
				if missingSeatRoleNeeded && !hasMissingRole {
					errRole := dg.GuildMemberRoleAdd(guildID, discordID, missingSeatRoleID)
					if errRole == nil && greetingChannel != "" {
						msg := fmt.Sprintf("<@%s>, you need to log into SeAT to receive your roles. Please visit %s", discordID, integ.SeatURL)
						_, _ = dg.ChannelMessageSend(greetingChannel, msg)
					}
				} else if !missingSeatRoleNeeded && hasMissingRole {
					_ = dg.GuildMemberRoleRemove(guildID, discordID, missingSeatRoleID)
				}
			}
```

- [x] **Step 4: Run tests**
Run: `go build ./roles/...`
Expected: Build passes.

- [x] **Step 5: Commit**
```bash
git add roles/roles.go
git commit -m "feat: enforce missing_seat_role on main characters"
```

### Task 3: Apply Missing Seat Role Logic to Guest/Alt Characters

**Files:**
- Modify: `roles/guest_roles.go`

**Interfaces:**
- Consumes: The same `missing_seat_role` logic.

- [x] **Step 1: Fetch config values**
In `roles/guest_roles.go` inside `SyncGuestRoles` where `greetingChannel` is fetched (around line 131):
```go
		var missingSeatRoleID string
		_ = db.DB.QueryRow("SELECT value FROM config WHERE guild_id = ? AND key = 'missing_seat_role'", guildID).Scan(&missingSeatRoleID)

		integ, _ := db.GetGuildIntegration(guildID)
```

- [x] **Step 2: Add logic to tracking requirement**
In `roles/guest_roles.go` inside the main `for _, cmap := range activeMappings` loop (around line 240), add:
```go
			missingSeatRoleNeeded := false
```

Inside the loop where `if seatNeededRoles[roleID] && !isSeatActive` is checked for corps (around line 251 and 278):
Change `if seatNeededRoles[roleID] && !isSeatActive {` to also set `missingSeatRoleNeeded = true`.

For alliance checks (around line 295):
Change `if seatNeededRoles[roleID] && !isSeatActive {` to also set `missingSeatRoleNeeded = true`.

- [x] **Step 3: Apply or remove the role per user**
At the end of the `cmap` user iteration (after standing roles loop), add:
```go
			if missingSeatRoleID != "" {
				hasMissingRole := HasRole(member.Roles, missingSeatRoleID)
				if missingSeatRoleNeeded && !hasMissingRole {
					errRole := dg.GuildMemberRoleAdd(guildID, discordID, missingSeatRoleID)
					if errRole == nil && greetingChannel != "" {
						msg := fmt.Sprintf("<@%s>, you need to log into SeAT to receive your roles. Please visit %s", discordID, integ.SeatURL)
						_, _ = dg.ChannelMessageSend(greetingChannel, greetMsg)
					}
				} else if !missingSeatRoleNeeded && hasMissingRole {
					_ = dg.GuildMemberRoleRemove(guildID, discordID, missingSeatRoleID)
				}
			}
```

- [x] **Step 4: Run tests**
Run: `go build ./roles/...`
Expected: Build passes.

- [x] **Step 5: Commit**
```bash
git add roles/guest_roles.go
git commit -m "feat: enforce missing_seat_role on guest characters"
```

### Task 4: Update Documentation

**Files:**
- Modify: `docs/commands.md`
- Modify: `docs/usage.md`

- [x] **Step 1: Add command to docs/commands.md**
Add the new command to the list in alphabetical or logical order:
```markdown
* `/set_missing_seat_role`: Set a role to assign to users who need to log into SeAT but haven't.
```

- [x] **Step 2: Add command to docs/usage.md**
Add the new command under the SeAT Integration section or relevant list:
```markdown
* `/set_missing_seat_role` to automatically tag users lacking SeAT registration and prompt them in the greeting channel.
```

- [x] **Step 3: Commit**
```bash
git add docs/commands.md docs/usage.md
git commit -m "docs: document /set_missing_seat_role"
```
