# rss-reader

Go-проект: агрегатор новостей из RSS-лент и Telegram-каналов. Telegram-бот для управления, HTTP API для чтения, MinIO для картинок.

## Быстрая навигация

- Подробная документация: `docs/PROJECT.md`
- Переменные окружения: `.env.example`

## Структура

```
cmd/bot/main.go       — Telegram-бот (управление источниками, команды)
cmd/parser/main.go    — RSS-парсер (фоновый обход по таймеру)
cmd/userbot/main.go   — MTProto юзербот (чтение Telegram-каналов)
cmd/api/main.go       — HTTP API сервер (GET /api/news)
internal/db/          — PostgreSQL: миграции, CRUD, модели
internal/bot/         — Telegram Bot API: команды, отправка
internal/parser/      — gofeed: парсинг RSS, извлечение контента/картинок
internal/userbot/     — gotd/td: MTProto-клиент, скачивание фото, загрузка в S3
internal/s3/          — MinIO: загрузка файлов, публичные URL
internal/api/         — net/http: cursor-пагинация, CORS, JSON
internal/notify/      — отправка уведомлений через Bot API (без зависимости от пакета bot)
```

## Ключевые библиотеки

| Пакет | Для чего |
|---|---|
| `github.com/jackc/pgx/v5` | PostgreSQL (connection pool) |
| `github.com/go-telegram-bot-api/telegram-bot-api/v5` | Telegram Bot API |
| `github.com/gotd/td` | Telegram MTProto (юзербот) |
| `github.com/mmcdole/gofeed` | Парсинг RSS/Atom фидов |
| `github.com/minio/minio-go/v7` | S3-совместимое хранилище |

## Docker Compose — 6 сервисов

`postgres`, `minio`, `bot`, `parser`, `userbot`, `api`

Единый `Dockerfile` с build arg `SERVICE` — компилирует нужный бинарник из `cmd/${SERVICE}`.

## Важные паттерны

- Все сервисы используют общую БД через `internal/db`
- Дедупликация статей: `UNIQUE(source_id, external_id)` + `ON CONFLICT DO NOTHING`
- Уведомления: parser и userbot отправляют напрямую через `internal/notify` (не через сервис бота)
- Сессия юзербота хранится в Docker volume, первый запуск требует `docker compose run -it userbot`
- API пагинация: cursor-based по полю `id` (after/since параметры)

## Сборка и запуск

```bash
cp .env.example .env  # заполнить
docker compose up -d
# Первый запуск юзербота — интерактивно:
docker compose run -it userbot
```

## При работе с кодом

- Go 1.22, CGO_ENABLED=0 (чистый Go, без SQLite)
- `go build ./cmd/bot && go build ./cmd/parser && go build ./cmd/api && go build ./cmd/userbot` — проверка сборки всех сервисов
- Тестов пока нет
- Миграции встроены в код (`internal/db/db.go`, функция `migrate`)
