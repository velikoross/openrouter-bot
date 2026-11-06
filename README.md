<h1 align="center">
    <img src="img/logo.png" width="220" />
    <div>
    OpenRouter
    <br>
    Bot
    </div>
</h1>

<h4 align="center">
    <strong>English (🇺🇸)</strong> | <a href="README_RU.md">Русский (🇷🇺)</a>
</h4>

This project allows you to launch your Telegram bot in a few minutes to communicate with free and paid AI models via [OpenRouter](https://openrouter.ai), or local LLMs, for example, via [LM Studio](https://lmstudio.ai).

> [!NOTE]
> This repository is a fork of the [openrouter-gpt-telegram-bot](https://github.com/deinfinite/openrouter-gpt-telegram-bot) project, which adds new features (such as switch current model and `Markdown` formatting in bot responses) and optimizes the container startup process.

<details>
    <summary>Example</summary>
    <img src="./img/example.png">
    <img src="./img/commands.png">
</details>

> Step-by-step server setup (in Russian): [DEPLOY_RU.md](DEPLOY_RU.md)

## Preparation

- Register with [OpenRouter](https://openrouter.ai) and get an [API key](https://openrouter.ai/settings/keys).

- Create your Telegram bot using [@BotFather](https://telegram.me/BotFather) and get its API token.

- Get your telegram id using [@getmyid_bot](https://t.me/getmyid_bot).

> [!TIP]
> When you launch the bot, you will be able to see the IDs of other users in the log, to whom you can also grant access to the bot in the future.

## Installation

To run locally on Windows or Linux system, download the pre-built binary (without dependencies) from the [releases](https://github.com/shevchukma/openrouter-bot/releases) page.

### Running in Docker

- Create a working directory:

```bash
mkdir openrouter-bot
cd openrouter-bot
```

- Create `.env` file and fill in the basic parameters:

```bash
# OpenRouter api key
API_KEY=
# Free modeles: https://openrouter.ai/models?max_price=0
MODEL=deepseek/deepseek-r1:free
# Telegram api key
TELEGRAM_BOT_TOKEN=
# Your Telegram id
ADMIN_IDS=
# List of users to access the bot, separated by commas
ALLOWED_USER_IDS=
# Disable guest access (enabled by default)
GUEST_BUDGET=0
# Language used for bot responses (supported: EN/RU)
BOT_LANG=EN
# Short names of the bot for group chats
BOT_ALIASES=bot
```

The list of all available parameters is listed in the [.env.example](https://github.com/shevchukma/openrouter-bot/blob/main/.env.example) file

- Run a container using the image from [Docker Hub](https://hub.docker.com/r/shevchukma/openrouter-bot):

```bash
docker run -d --name OpenRouter-Bot \
    -v ./.env:/openrouter-bot/.env \
    -v ./logs:/openrouter-bot/logs \
    --restart unless-stopped \
    velikoross/openrouter-bot:latest
```

The image is build for `amd64` and `arm64` (Raspberry Pi) platforms using [docker buildx](https://github.com/docker/buildx).

## Group chats

In a group the bot answers when it is addressed:

- with `/ask question` (or `/pirdun question`); `/ask` sent as a reply to someone's message asks about that message;
- with a mention `@bot_name question`;
- with a short name from `BOT_ALIASES`, e.g. `Pirdun, tell a joke` (case-insensitive, must be a separate word);
- by replying to one of the bot's messages.

Other messages are ignored, as are commands for other bots (`/ask@other_bot`).

> [!IMPORTANT]
> Bots have **privacy mode** enabled by default: in groups they only see commands with `@bot_name`, mentions and replies. Because of that plain `/ask` and short names may never reach the bot. Disable it: [@BotFather](https://t.me/BotFather) → `/setprivacy` → choose the bot → `Disable`, then **remove the bot from the group and add it again**. Alternatively make the bot a group admin. The bot logs on startup whether privacy mode is on.

Conversation history is kept per user per chat.

## Switching the model from Telegram (admin only)

Only users from `ADMIN_IDS` can use these commands (everyone else is refused, buttons included):

| Command | Action |
| --- | --- |
| `/model` | Show current model and the model from config |
| `/set_model` | Buttons: `MODEL_PRESETS`, recently used models and "default" |
| `/set_model <id>` | Set a model (validated against the OpenRouter model list, similar ids are suggested on typos) |
| `/set_model default` | Return to `MODEL` / `config.yaml` |
| `/get_models` | List free models |
| `/get_models <text>` | Search all models with prices |

The selected model applies to all chats and is saved to `logs/bot_state.json`, so it survives restarts and `config.yaml` reloads. Mount the `logs` directory as a volume (see `docker-compose.yml`) to keep it when the container is recreated.

## Tests

```bash
go test -race ./...
```

## Build

```bash
git clone https://github.com/shevchukma/openrouter-bot
cd openrouter-bot
docker-compose up -d --build
```
