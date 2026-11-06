# Запуск на новом сервере

Инструкция для Linux-сервера (Ubuntu/Debian) с Docker. Бот работает через long polling,
поэтому ни домен, ни открытые порты, ни HTTPS ему не нужны — только исходящий интернет.

## 1. Подготовка (один раз)

1. **Остановите старый экземпляр бота**, если он где-то ещё запущен. Два процесса с одним
   токеном мешают друг другу: в логах будет `Conflict: terminated by other getUpdates request`,
   и бот «иногда не отвечает».
2. **Privacy mode — по желанию.** С включённым (по умолчанию) в группе работают `/ask@имя_бота`,
   ответы (reply) на сообщения бота, упоминания и `/ask` без `@`, если бот в группе один.
   Не работают только короткие имена (`Пирдун, …`) — Telegram такие сообщения боту не присылает.
   Если они нужны: @BotFather → `/setprivacy` → бот → `Disable`, затем удалите бота из группы
   и добавьте заново. Учтите, что тогда бот получает все сообщения группы (отвечает он
   всё равно только на обращения).
3. Узнайте свой Telegram ID у [@getmyid_bot](https://t.me/getmyid_bot) — он пойдёт в `ADMIN_IDS`.
4. Ключ OpenRouter: <https://openrouter.ai/settings/keys>.

## 2. Установка Docker

```bash
curl -fsSL https://get.docker.com | sudo sh
```

```bash
sudo usermod -aG docker $USER && newgrp docker
```

## 3. Каталог и настройки

```bash
mkdir -p ~/openrouter-bot/logs && cd ~/openrouter-bot
```

Создайте `~/openrouter-bot/.env` (подставьте свои значения):

```bash
cat > .env <<'CONF'
TELEGRAM_BOT_TOKEN=123456789:AA...
API_KEY=sk-or-v1-...

# Ваш Telegram ID — только вы сможете менять модель
ADMIN_IDS=111111111
# Кому ещё разрешён бот (через запятую), бюджет USER_BUDGET в $ (0 = без лимита)
ALLOWED_USER_IDS=
USER_BUDGET=0
# Гости (все остальные): 0 = запрещено
GUEST_BUDGET=0

MODEL=deepseek/deepseek-chat-v3-0324:free
# Кнопки в /set_model
MODEL_PRESETS=deepseek/deepseek-chat-v3-0324:free,openai/gpt-4o-mini

BOT_LANG=RU
# Короткие имена для групп
BOT_ALIASES=пирдун,бот

VISION=false
MAX_HISTORY_SIZE=20
MAX_HISTORY_TIME=120
CONF
chmod 600 .env
```

Создайте `~/openrouter-bot/docker-compose.yml`:

```bash
cat > docker-compose.yml <<'CONF'
services:
  openrouter-bot:
    container_name: openrouter-bot
    image: velikoross/openrouter-bot:latest
    volumes:
      - ./.env:/openrouter-bot/.env:ro
      - ./logs:/openrouter-bot/logs
    restart: unless-stopped
    logging:
      driver: json-file
      options:
        max-size: "10m"
        max-file: "3"
CONF
```

Каталог `logs` хранит статистику расходов и выбранную через `/set_model` модель
(`logs/bot_state.json`) — благодаря volume они переживают обновление контейнера.

## 4. Запуск

```bash
docker compose up -d
```

```bash
docker compose logs -f
```

В логе должно быть:

- `Authorized as @ИмяБота` — токен верный;
- `Privacy mode is ON` — нормально, если короткие имена в группах не нужны (см. шаг 1.2);
- при сообщении боту — строка вида `[private 111111111] @you (id ...): "привет"`.

Проверка в Telegram: напишите боту в личку, затем `/model` и `/set_model` (кнопки).
В группе: `/ask привет`, `@ИмяБота привет`, reply на ответ бота; `Пирдун, привет` — только с выключенным privacy mode.

## 5. Обновление

```bash
cd ~/openrouter-bot && docker compose pull && docker compose up -d
```

Новый образ собирается в GitHub: Actions → CI → Run workflow (`Docker` = true).

## 6. Частые проблемы

| Симптом | Причина |
| --- | --- |
| `Telegram login failed` | неверный `TELEGRAM_BOT_TOKEN` |
| `Conflict: terminated by other getUpdates request` | бот запущен где-то ещё (старый сервер) |
| В группе не реагирует на короткое имя | включён privacy mode (или после отключения бот не перезаходил в группу) |
| В группе `/ask` без `@` не работает | в группе несколько ботов и privacy mode включён — пишите `/ask@ИмяБота` или выберите команду из меню |
| «У вас нет бюджета» | пользователь не в `ADMIN_IDS`/`ALLOWED_USER_IDS`, а `GUEST_BUDGET=0` |
| «Ошибка обработки запроса: 429…» (видит админ) | лимит бесплатной модели — выберите другую через `/set_model` |
| «Ошибка …: 401» | неверный `API_KEY` |
| `exec format error` | образ не под архитектуру сервера (собирается amd64 и arm64) |

## Без Docker

Скачайте архив `openrouter-bot-linux` (или `-raspberry-pi`) из артефактов сборки в GitHub Actions,
распакуйте, заполните `.env` и запустите `./openrouter-bot`. Для автозапуска — unit systemd:

```ini
[Unit]
Description=OpenRouter Telegram bot
After=network-online.target
Wants=network-online.target

[Service]
WorkingDirectory=/opt/openrouter-bot
ExecStart=/opt/openrouter-bot/openrouter-bot
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
```
