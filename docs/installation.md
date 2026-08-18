# Installation Guide

## Prerequisites
* Go 1.21+

## 1. Credentials
1. Create an application on the EVE Online Developer portal with callback URL `http://localhost:7887/callback` (or `http://localhost:7887/auth/eve/callback` for SSO).
2. Create a bot on the Discord Developer portal and invite it to your server.

## 2. Setup
1. Clone the repository.
2. Copy `.env.example` to `.env` and fill in your credentials.

## 3. (Optional) Custom Emojis
Configure custom emojis to display icons in Discord instead of plain text headers.
1. Upload the PNG assets from the `icons/` directory to your Discord server.
2. Add the following to your `.env` file using the emoji ID or format:
   * `EVE_MAIL_EMOJI` (or `DISCORD_MAIL_EMOJI`)
   * `EVE_CALENDAR_EMOJI` (or `DISCORD_CALENDAR_EMOJI`)

## 4. Running the Bot

### Option A: Local / Direct
1. Run `make build` to compile the binary.
2. Run `make up` to start the bot locally (or `go run main.go`).

### Option B: Systemctl Service (Linux)
Run the bot as a background service to ensure it restarts automatically on failure or reboot.

1. Build the binary: `make build`
2. Create a new service file: `sudo nano /etc/systemd/system/cngbot.service`
3. Add the following configuration (replace `your_username` and `/path/to/cngbot`):

```ini
[Unit]
Description=CNG Discord Bot
After=network.target

[Service]
Type=simple
User=your_username
WorkingDirectory=/path/to/cngbot
ExecStart=/path/to/cngbot/CNGBot
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
```

4. Apply the configuration and start the service:
```bash
sudo systemctl daemon-reload
sudo systemctl enable cngbot
sudo systemctl start cngbot
```