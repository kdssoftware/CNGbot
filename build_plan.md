# Build Plan: EVE Online Donations Tracking & Leaderboard

## 1. Overview
This document outlines the implementation plan for a new Discord bot feature that tracks EVE Online corporate donations, posts real-time thank-you messages, and provides a leaderboard of top donors. The feature maps EVE characters to Discord users and EVE corporations to Discord roles where possible.

## 2. Database Schema Updates
To support this feature, the database needs to store configuration and track donations to avoid duplicate announcements.

### New Tables / Models:
1.  **GuildDonationSettings**
    *   `guild_id` (Primary Key)
    *   `channel_id` (Nullable): The channel where announcements and leaderboards are posted. If null, the feature is disabled for the guild.
2.  **TrackedCorporations**
    *   `guild_id` (Foreign Key)
    *   `role_id`: The Discord role ID mapped to the EVE Corporation.
    *   `eve_corp_id`: The corresponding EVE Corporation ID.
3.  **DonationRecord**
    *   `transaction_id` (Primary Key, from ESI): Unique ID of the wallet journal entry.
    *   `receiver_corp_id` (Index): The tracked corporation that received the ISK.
    *   `donor_id` (Index): The EVE character or corporation ID that donated.
    *   `donor_type`: 'character' or 'corporation'.
    *   `amount`: Total ISK donated in this transaction.
    *   `date`: Timestamp of the donation.

## 3. Discord Slash Commands
Implement the following slash commands using the framework's command registration.

### 3.1 `/set_donations_channel`
*   **Description**: Sets or disables the channel for donation announcements.
*   **Arguments**:
    *   `channel` (Optional, Channel Type): The channel to post in.
*   **Logic**:
    *   If `channel` is provided: Upsert `GuildDonationSettings` with the `channel_id`.
    *   If `channel` is omitted: Set `channel_id` to null or delete the `GuildDonationSettings` record to turn off the feature.
*   **Response**: Confirm the new setting to the user.

### 3.2 `/set_donations_track_corporation`
*   **Description**: Toggles tracking for a specific corporation via its mapped Discord role.
*   **Arguments**:
    *   `role` (Required, Role Type): The role mapped to the EVE Corporation.
*   **Logic**:
    *   Look up the `eve_corp_id` associated with the given Discord `role`.
    *   If a record exists in `TrackedCorporations` for this guild and role, remove it (stop tracking).
    *   If it does not exist, add it (start tracking).
*   **Response**: Confirm whether the corporation was added or removed from tracking.

### 3.3 `/donations_leaderboard`
*   **Description**: Displays the top 50 donors of all time to the tracked corporations.
*   **Arguments**: None.
*   **Logic**:
    *   Aggregate total ISK and count of donations from `DonationRecord` for all `receiver_corp_id`s tracked by the guild.
    *   Sort descending by total ISK.
    *   Limit to top 50.
    *   Format the output according to the Formatting Rules below.
*   **Response**: Send the formatted leaderboard as a message (or embed).

### 3.4 `/post_donations_leaderboard`
*   **Description**: Posts the leaderboard to the configured donations channel.
*   **Arguments**: None.
*   **Logic**:
    *   Fetch `channel_id` from `GuildDonationSettings`.
    *   If not set, return an error message to the user.
    *   Generate the leaderboard exactly as in `/donations_leaderboard`.
    *   Send the generated leaderboard to the configured `channel_id`.
*   **Response**: Ephemeral confirmation to the user that the leaderboard was posted.

## 4. Background Task (ESI Polling)
*   **Loop**: Run a background task periodically (e.g., every 15-30 minutes, respecting ESI caching limits).
*   **Action**:
    *   Iterate through all unique `eve_corp_id`s in `TrackedCorporations`.
    *   Fetch the corporation wallet journal from ESI: `GET /corporations/{corporation_id}/wallets/{division}/journal/`. (Note: Requires valid ESI tokens with `esi-wallet.read_corporation_wallets.v1` scope for a director of the corp).
    *   Filter for `ref_type` corresponding to player donations (typically `player_donation` or similar).
    *   For each record, check if `transaction_id` already exists in `DonationRecord`.
    *   If it's new:
        1.  Insert into `DonationRecord`.
        2.  Fetch `GuildDonationSettings` for guilds tracking this corp.
        3.  Construct a thank-you message (see Formatting Rules).
        4.  Post the message to the configured `channel_id`.

## 5. Formatting Rules (CRITICAL)
The LLM must strictly adhere to these formatting rules for all outputs (Leaderboards and Announcements).

### Constraints:
*   **NO EMOJIS**.
*   **NO DASHES** (`-`). Use alternative separators like bullet points (`*`), commas, or periods if necessary.
*   **Clean aesthetic**: The leaderboard must be a good-looking, structured text block (using Markdown code blocks or structured embeds if applicable, but avoiding messy characters).

### Entity Resolution & Display Logic:
For every donor (Character or Corporation):

**If Character:**
1.  Check if the EVE character is mapped to a Discord user in the database.
2.  If Mapped: Display the Discord user ping (e.g., `<@USER_ID>`).
3.  If Not Mapped: Display the EVE character's name formatted as a Markdown link to their EVEWho page: `[Character Name](https://evewho.com/character/CHARACTER_ID)`

**If Corporation:**
1.  Check if the EVE corporation is mapped to a Discord role in the database.
2.  If Mapped: Display the Discord role ping (e.g., `<@&ROLE_ID>`).
3.  If Not Mapped: Display the EVE corporation's name formatted as a Markdown link to their EVEWho page: `[Corporation Name](https://evewho.com/corporation/CORP_ID)`

### Leaderboard Specifics:
*   Title/Header explaining what the leaderboard represents (e.g., "Top 50 Donors All Time").
*   For each of the top 50 entities, show:
    *   The resolved entity name/ping (from rules above).
    *   Total ISK donated (formatted clearly, e.g., `1,000,000,000 ISK`).
    *   Number of times donated (e.g., `(5 donations)`).

### Announcement Specifics:
*   Triggered on new donations.
*   Message must thank the donor.
*   Use the exact Entity Resolution logic above for the donor.
*   Example: "Thank you to [Resolved Entity] for the generous donation of [Amount] ISK!"
