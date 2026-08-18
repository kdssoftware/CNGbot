# Multi-Tenant SaaS Build Plan

This document outlines the architecture and steps required to convert the CNGBot from a single-tenant script into a multi-tenant service capable of supporting hundreds of independent Discord servers concurrently.

## 1. Discord Developer Portal Setup
Before writing code, the following must be configured in the [Discord Developer Portal](https://discord.com/developers/applications):
* **OAuth2 Redirects:** Add your web server's callback URL (e.g., `http://localhost:7887/auth/discord/callback` for dev, and `https://yourdomain.com/auth/discord/callback` for prod) to the OAuth2 -> General settings.
* **OAuth2 URL Generator:** Generate a Bot Invite Link with the scopes `bot` and `applications.commands`, and permissions to Manage Roles and Send Messages.
* **Client Secret:** Obtain the Discord `CLIENT_ID` and `CLIENT_SECRET`. These will be added to the `.env` file alongside the bot token.

## 2. Environment Variables (.env)
The `.env` file will be stripped of all guild-specific settings. It will only contain application-wide secrets:
```env
DISCORD_TOKEN=
DISCORD_CLIENT_ID=
DISCORD_CLIENT_SECRET=
DISCORD_OAUTH_REDIRECT=http://localhost:7887/auth/discord/callback
EVE_CLIENT_ID=
EVE_SECRET=
EVE_CALLBACK_URL=http://localhost:7887/auth/eve/callback
PORT=7887
EVE_MAIL_EMOJI=
EVE_CALENDAR_EMOJI=
```

## 3. Database Schema Updates
Move integration variables into the SQLite database, keyed by `guild_id`.
* **New Table `guild_integrations`**:
  * `guild_id` TEXT (Primary Key)
  * `eve_character_id` INTEGER (The character used to fetch standings and mail)
  * `eve_refresh_token` TEXT (The OAuth2 token for this specific server)
  * `seat_url` TEXT
  * `seat_api_key` TEXT
* **Table `mail_state`**:
  * `guild_id` TEXT (Primary Key)
  * `last_mail_id` INTEGER

## 4. Web Dashboard & OAuth2 Flows
Build a lightweight web server alongside the bot (using standard `net/http`) to handle user onboarding.
* **Session Management:** Implement secure cookies to track logged-in users.
* **Discord OAuth2 Login:** Users log into the site. Request `identify` and `guilds` scopes. Filter the guilds list to show only servers where the user has `Administrator` permissions.
* **Server Dashboard:** A page to configure a specific server.
* **EVE ESI OAuth2:** A button to "Link EVE Character". Redirects to EVE SSO requesting required scopes (`esi-mail.read_mail.v1`, `esi-characters.read_contacts.v1`, etc.). On callback, store the `refresh_token` and `character_id` in `guild_integrations`.
* **SeAT Configuration:** An HTML form on the dashboard to input `seat_url` and `seat_api_key`.

## 5. Refactoring API Clients
The ESI and SeAT clients must become context-aware.
* **`esi.go`**: Update `GetOAuthHTTPClient(guildID string)` to query the database for that guild's `eve_refresh_token`.
* **`seat.go`**: Update `GetValidSeatCharacters(guildID string)` to query the database for that guild's SeAT credentials.

## 6. Background Worker Isolation
The background loops must execute per-server instead of globally.
* **Mail Polling (`mail.go`)**: 
  * Query the database for all `guild_id`s that have an `eve_refresh_token` and a configured `mail_channel`.
  * Loop through each guild, instantiate their specific OAuth client, and fetch their inbox. Update `last_mail_id` in the database.
* **Roles & Standings (`roles.go`)**:
  * Iterate over each `guild_id`. 
  * Fetch standings using that specific guild's `eve_character_id` and OAuth client (so Server A uses Server A's CEO, Server B uses Server B's CEO).
  * Query the SeAT API using that guild's specific SeAT credentials.

## 7. Discord Bot Commands
* **Dashboard Link:** Add a `/dashboard` slash command that replies with a direct link to the web dashboard for configuration.
* **Permissions:** Native Discord permissions should be utilized.
