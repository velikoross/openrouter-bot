<h1 align="center">
    <img src="img/logo.png" width="220" />
    <div>
    OpenRouter
    <br>
    Bot
    </div>
</h1>

<h4 align="center">
    <a href="README.md">English (🇺🇸)</a> | <strong>Русский (🇷🇺)</strong>
</h4>

Этот проект позволяет за несколько минут запустить своего Telegram бота для общения с бесплатными и платными моделями ИИ через [OpenRouter](https://openrouter.ai), или локальными LLM, например, через [LM Studio](https://lmstudio.ai).

> [!NOTE]
> Этот репозиторий является форком проекта [openrouter-gpt-telegram-bot](https://github.com/deinfinite/openrouter-gpt-telegram-bot), который добавляет новые функции (например, изменение текущей модели и форматирование `Markdown` в ответах бота) и оптимизирует процесс запуска в контейнере.

<details>
    <summary>Пример</summary>
    <img src="./img/example.png">
    <img src="./img/commands.png">
</details>

## Подготовка

- Зарегистрируйтесь в [OpenRouter](https://openrouter.ai) и получите [API ключ](https://openrouter.ai/settings/keys).

- Создайте своего Telegram бота, используя [@BotFather](https://telegram.me/BotFather) и получите его API токен.

- Получите свой Telegram id, используя [@getmyid_bot](https://t.me/getmyid_bot).

> [!TIP]
> При запуске бота вы сможете увидеть в логах идентификаторы других пользователей, которым вы также сможете предоставить доступ к боту в дальнейшем.

## Установка

Для локального запуска в системе Windows или Linux, загрузите предварительно собранный бинарный файл (без зависимостей) на странице [релизов](https://github.com/shevchukma/openrouter-bot/releases).

### Запуск в Docker

- Создайте рабочий каталог бота:

```bash
mkdir openrouter-bot
cd openrouter-bot
```

- Создайте `.env` файл и заполните базовые параметры:

```bash
# OpenRouter api key
API_KEY=
# Список бесплатных моделей: https://openrouter.ai/models?max_price=0
MODEL=deepseek/deepseek-r1:free
# Telegram api key
TELEGRAM_BOT_TOKEN=
# Ваш Telegram id
ADMIN_IDS=
# Список пользователей для доступа к боту, разделенных запятыми
ALLOWED_USER_IDS=
# Отключить гостевой доступ (включен по умолчанию)
GUEST_BUDGET=0
# Язык, используемый в ответах бота
BOT_LANG=RU
# Короткие имена бота для групп
BOT_ALIASES=пирдун,бот
```

Список всех доступных параметров приведен в файле [.env.example](https://github.com/shevchukma/openrouter-bot/blob/main/.env.example)

- Запустите контейнер, используя образ из [Docker Hub](https://hub.docker.com/r/shevchukma/openrouter-bot):

```bash
docker run -d --name OpenRouter-Bot \
    -v ./.env:/openrouter-bot/.env \
    -v ./logs:/openrouter-bot/logs \
    --restart unless-stopped \
    velikoross/openrouter-bot:latest
```

Образ собран для платформ `amd64` и `aarch64` (Raspberry Pi) с использованием [docker buildx](https://github.com/docker/buildx).

## Работа в группах

В группе бот отвечает, когда к нему обращаются:

- командой `/ask вопрос` (или `/pirdun вопрос`); `/ask` в ответ на чьё-то сообщение — спросить про это сообщение;
- упоминанием `@имя_бота вопрос`;
- коротким именем из `BOT_ALIASES`, например `Пирдун, расскажи анекдот` (регистр не важен, слово должно стоять отдельно);
- ответом (reply) на сообщение бота.

Остальные сообщения игнорируются, команды для других ботов (`/ask@other_bot`) тоже.

> [!IMPORTANT]
> По умолчанию у ботов включён **privacy mode**: в группе бот видит только команды с `@имя_бота`, упоминания и ответы на свои сообщения. Из-за этого `/ask` без `@имя_бота` и короткие имена могут не доходить до бота. Отключите его: [@BotFather](https://t.me/BotFather) → `/setprivacy` → выберите бота → `Disable`, затем **удалите бота из группы и добавьте заново**. Альтернатива — сделать бота администратором группы. При запуске бот пишет в лог, включён ли privacy mode.

История диалога хранится отдельно для каждого пользователя в каждом чате.

## Смена модели из Telegram (только админ)

Команды доступны только пользователям из `ADMIN_IDS` (остальные получают отказ, в том числе при нажатии кнопок):

| Команда | Действие |
| --- | --- |
| `/model` | Текущая модель и модель из конфига |
| `/set_model` | Кнопки выбора: `MODEL_PRESETS`, недавно выбранные модели и «по умолчанию» |
| `/set_model <id>` | Установить модель (id проверяется по списку моделей OpenRouter, при опечатке предлагаются похожие) |
| `/set_model default` | Вернуть модель из `MODEL` / `config.yaml` |
| `/get_models` | Список бесплатных моделей |
| `/get_models <текст>` | Поиск по всем моделям с ценами |

Выбранная модель действует для всех чатов и сохраняется в `logs/bot_state.json`, поэтому переживает перезапуск и перезагрузку `config.yaml`. Чтобы данные не терялись при пересоздании контейнера, смонтируйте каталог `logs` (см. `docker-compose.yml`).

## Тесты

```bash
go test -race ./...
```

## Сборка

```bash
git clone https://github.com/shevchukma/openrouter-bot
cd openrouter-bot
docker-compose up -d --build
```
