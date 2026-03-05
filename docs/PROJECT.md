# RSS Reader — полная документация проекта

## 1. Обзор

Агрегатор новостей на Go, собирающий контент из двух типов источников:

- **RSS/Atom ленты** — через HTTP-парсинг (библиотека gofeed)
- **Telegram-каналы** — через MTProto юзербот (библиотека gotd/td)

Управление через Telegram-бота. Чтение через HTTP API. Картинки из Telegram хранятся в S3 (MinIO).

Дополнительные возможности:
- **AI-обогащение** — каждая новая статья автоматически получает AI-заголовок и краткое изложение через OpenRouter
- **Семантическая дедупликация** — одинаковые новости из разных источников объединяются через косинусное сходство
- **Анализ медиа** — бот может анализировать фото, видео и YouTube-ссылки через Google Gemini и сохранять результаты в Obsidian

---

## 2. Архитектура

### 2.1. Сервисы

Проект разбит на 4 Go-сервиса + 2 инфраструктурных контейнера:

```
                    +-------------+
                    |  PostgreSQL  |  <-- общая БД для всех сервисов
                    +------+------+
                           |
          +----------------+----------------+----------------+
          |                |                |                |
    +-----+-----+   +-----+-----+   +------+----+   +------+----+
    |    bot     |   |   parser  |   |  userbot   |   |    api    |
    | (Telegram  |   | (RSS по   |   | (MTProto,  |   | (HTTP     |
    |  команды)  |   |  таймеру) |   |  каналы)   |   |  JSON)    |
    +------------+   +-----+-----+   +-----+------+   +-----------+
          |                |               |
    +-----+-----+   +------+------+  +-----+-----+
    |  Gemini   |   | OpenRouter  |  |   MinIO    |
    | (медиа-   |   | (AI-обогащ) |  | (картинки) |
    |  анализ)  |   +-------------+  +-----------+
    +-----------+
```

**bot** — принимает команды от пользователей, записывает источники в БД. Опционально анализирует медиафайлы через Gemini.
**parser** — каждые N минут читает RSS-источники из БД, парсит ленты, сохраняет новые статьи через processor.
**userbot** — каждые N минут читает Telegram-источники из БД, подключается к каналам через MTProto, скачивает посты и фото.
**api** — отдает статьи по HTTP с cursor-пагинацией, включая AI-поля.

Сервисы **не общаются** друг с другом напрямую. Вся координация — через общую БД.

### 2.2. Единый Dockerfile

```dockerfile
FROM golang:1.22-alpine AS builder
ARG SERVICE                          # bot | parser | userbot | api
COPY . .
RUN CGO_ENABLED=0 go build -o service ./cmd/${SERVICE}

FROM alpine:3.19
COPY --from=builder /app/service .
CMD ["./service"]
```

В docker-compose для каждого сервиса указан `build.args.SERVICE`.

### 2.3. Потоки данных

```
Пользователь --(/add, /addchannel)--> bot --> INSERT INTO sources

parser (ticker) --> SELECT sources WHERE type='rss' --> HTTP GET RSS
  --> processor.Process()
       ├── RecentArticlesForSimilarity (2000 статей за 7 дней)
       ├── similarity.Cosine() -- проверка дублей
       ├── INSERT INTO articles (или UPDATE если дубль с бо́льшим текстом)
       └── enrichAsync() --> OpenRouter --> UPDATE ai_title, summary

userbot (ticker) --> SELECT sources WHERE type='telegram' --> MTProto GetHistory
  --> processor.Process() (та же цепочка)
       |
  download photo --> MinIO

Пользователь --> YouTube/фото/видео --> bot --> Gemini --> Obsidian .md файл

Фронтенд/клиент --> GET /api/news --> SELECT FROM articles --> JSON (с ai_title, summary)
```

---

## 3. База данных

PostgreSQL 16. Миграции выполняются автоматически при запуске любого сервиса (функция `migrate` в `internal/db/db.go`).

### 3.1. Таблица `sources`

| Колонка | Тип | Описание |
|---|---|---|
| `id` | SERIAL PK | Автоинкремент |
| `url` | TEXT UNIQUE | Для RSS — URL ленты. Для Telegram — username канала или invite-хэш (с префиксом `+`) |
| `name` | TEXT | Человекочитаемое название (опционально) |
| `source_type` | TEXT | `'rss'` или `'telegram'` |
| `chat_id` | BIGINT | Telegram chat_id пользователя, добавившего источник |
| `created_at` | TIMESTAMPTZ | Дата добавления |

**Примеры url для Telegram-каналов:**
- `durov` — публичный канал @durov
- `+abc123def` — приватный канал по invite-ссылке

### 3.2. Таблица `articles`

| Колонка | Тип | Описание |
|---|---|---|
| `id` | SERIAL PK | Автоинкремент, используется как cursor для пагинации |
| `source_id` | INTEGER FK | Ссылка на sources(id), ON DELETE CASCADE |
| `external_id` | TEXT | Уникальный ID внутри источника: для RSS — URL статьи, для Telegram — message ID |
| `title` | TEXT | Оригинальный заголовок статьи (для TG — первая строка поста) |
| `content` | TEXT | Полный текст |
| `url` | TEXT | Ссылка на оригинал: RSS link или `https://t.me/channel/msg_id` |
| `image_url` | TEXT | Ссылка на картинку: для RSS — из фида, для TG — MinIO URL |
| `pub_date` | TIMESTAMPTZ | Дата публикации |
| `created_at` | TIMESTAMPTZ | Дата сохранения в нашей БД |
| `ai_title` | TEXT | AI-сгенерированный заголовок (OpenRouter, опционально) |
| `summary` | TEXT | AI-сгенерированное краткое изложение (OpenRouter, опционально) |

Колонки `ai_title` и `summary` добавляются через `ALTER TABLE ... ADD COLUMN IF NOT EXISTS` — безопасно для существующих БД.

**Уникальность:** `UNIQUE(source_id, external_id)` — предотвращает дублирование точных копий. Семантические дубли из разных источников обрабатываются в `processor` до вставки.

**Индексы:**
- `idx_articles_created_at` — по `id DESC` (для пагинации)
- `idx_articles_source_id` — по `source_id` (для каскадных операций)

### 3.3. Пагинация (cursor-based)

Классический подход для бесконечного скролла. Поле `id` монотонно возрастает, поэтому:

```
Свежие:            WHERE id > :since  ORDER BY id ASC   -- для pull-to-refresh
Старые (скролл):   WHERE id < :after  ORDER BY id DESC  -- для подгрузки вниз
Последние:         ORDER BY id DESC LIMIT :limit         -- начальная загрузка
```

Клиент запоминает `id` первой и последней статьи из ответа и использует их как параметры для следующего запроса.

---

## 4. Сервисы — детально

### 4.1. bot (`internal/bot/bot.go`)

**Технология:** go-telegram-bot-api v5 (Bot API через long polling).

**Команды:**

| Команда | Описание |
|---|---|
| `/start`, `/help` | Справка (содержимое адаптируется под наличие Gemini) |
| `/add <url> [название]` | Добавить RSS-источник |
| `/addchannel <ссылка или @username>` | Добавить Telegram-канал |
| `/list` | Все источники с типами [RSS]/[TG] |
| `/remove <id>` | Удалить источник (статьи удаляются каскадно) |
| `/news` | Последние 10 статей (предпочитается `ai_title` если есть) |
| `/analyze <youtube-url>` | Анализ YouTube-видео через Gemini (только если `GEMINI_API_KEY` задан) |

**Неявные триггеры (только при наличии Gemini):**

| Что прислал пользователь | Действие |
|---|---|
| Фото / скриншот | Анализ через Gemini, сохранение в Obsidian |
| Видеофайл (mp4, mov, avi, mkv, webm, 3gp) | Загрузка + анализ через Gemini, сохранение в Obsidian |
| Документ с видео MIME-типом | То же что видеофайл |
| Сообщение с YouTube-ссылкой | Анализ видео напрямую через Gemini Files API |

Результат анализа сохраняется как `.md`-файл в `{OBSIDIAN_VAULT_PATH}/inbox/` с YAML frontmatter (теги, дата, источник).

**Нормализация ссылок на каналы** (функция `normalizeChannelRef`):
- `https://t.me/channel` -> `channel`
- `@channel` -> `channel`
- `https://t.me/+hash` -> `+hash`
- `https://t.me/joinchat/hash` -> `+hash`

**Обработка дубликатов:** `AddSource` возвращает `bool`. Если URL уже есть — бот говорит "уже добавлен" вместо ложного "добавлено".

**Команда `/news`:** показывает `ai_title` если есть, иначе оригинальный `title`, иначе начало `content`.

### 4.2. parser (`internal/parser/parser.go`)

**Технология:** gofeed + net/http.

**Цикл работы:**
1. Стартует, сразу делает первый обход
2. Ждёт тикер (интервал из env `PARSE_INTERVAL_MIN`, по умолчанию 15 мин)
3. Загружает все sources с type='rss'
4. Для каждого: HTTP GET -> gofeed.Parse -> для каждого item -> `processor.Process()`
5. Новые статьи отправляются в NOTIFY_CHAT_ID через `internal/notify`

**HTTP таймаут:** 30 секунд на каждый фид.

**Извлечение данных из RSS item:**
- `external_id` = item.Link
- `title` = item.Title
- `content` = item.Content (если есть) или item.Description
- `image_url` = item.Image.URL (если есть)
- `url` = item.Link

Сохранение и дедупликация выполняются через `processor.Process()` — не напрямую в БД.

### 4.3. userbot (`internal/userbot/userbot.go`)

**Технология:** gotd/td (чистый Go MTProto).

Самый сложный сервис. Подключается к Telegram как обычный пользователь (не бот), что позволяет читать любые каналы.

**Авторизация:**
- При первом запуске требуется интерактивный ввод: номер телефона -> код из Telegram -> (опционально) пароль 2FA
- Запуск: `docker compose run -it userbot`
- Сессия сохраняется в файл `/data/userbot.session` (Docker volume `userbotdata`)
- При последующих запусках сессия загружается автоматически
- Реализация: `termAuth` (структура, имплементирующая `auth.UserAuthenticator`)

**Типы каналов и их обработка:**

| Тип | Как хранится в sources.url | Обработка |
|---|---|---|
| Публичный (`@channel`) | `channel` | `ContactsResolveUsername` -> `ChannelsJoinChannel` (если не состоит) -> `MessagesGetHistory` |
| Приватный (invite link) | `+hash` | `MessagesCheckChatInvite` -> `MessagesImportChatInvite` (join) -> `MessagesGetHistory` |
| Приватный (join by request) | `+hash` | `CheckChatInvite` -> если `RequestNeeded=true` -> логируется warning, пропускается |

**Обработка invite:**
- `ChatInviteAlready` — уже состоим, извлекаем channel, читаем историю
- `ChatInvite` — не состоим. Если `RequestNeeded` — пропускаем. Иначе — `ImportChatInvite` -> join -> читаем
- `ChatInvitePeek` — можем подсмотреть, извлекаем channel, читаем

**Скачивание фото:**
1. Проверяется `msg.Media` -> `MessageMediaPhoto` -> `Photo`
2. Выбирается самый большой PhotoSize (по W*H)
3. Создается `InputPhotoFileLocation` с нужным `ThumbSize`
4. `downloader.NewDownloader().Download(api, location).Stream(ctx, writer)` — стримит байты
5. Загрузка в MinIO: ключ `images/{source_id}/{message_id}.jpg`
6. В статью записывается публичный URL MinIO

**Извлечение данных из Telegram-сообщения:**
- `external_id` = message ID (строка)
- `title` = первая строка текста (до 100 символов)
- `content` = полный текст сообщения
- `url` = `https://t.me/{username}/{msg_id}` (для публичных каналов)
- `image_url` = MinIO URL (если есть фото)
- `pub_date` = Unix timestamp сообщения

Сохранение через `processor.Process()` — не напрямую в БД.

**Лимит:** 50 последних сообщений за один обход канала.

### 4.4. processor (`internal/processor/processor.go`)

Промежуточный слой между парсерами (parser, userbot) и БД. Оба сервиса вызывают `processor.Process()` вместо прямого `db.SaveArticle`.

**Что делает `Process()`:**
1. Загружает до 2000 последних статей за 7 дней (`RecentArticlesForSimilarity`)
2. Вычисляет косинусное сходство новой статьи с каждой из них (`similarity.Cosine`)
3. Если найден дубль (сходство ≥ 0.55):
   - Новая статья длиннее — обновляет существующую (`UpdateArticleContent`), запускает AI-обогащение
   - Существующая длиннее — пропускает новую (`ResultDuplicate`)
4. Если дублей нет — сохраняет через `SaveArticleFull` (с `ON CONFLICT DO NOTHING`)
5. Для новых статей запускает `enrichAsync()` в горутине

**Результаты:**
- `ResultNew` — статья сохранена
- `ResultDuplicate` — статья пропущена (семантический или точный дубль)
- `ResultReplaced` — существующая статья обновлена более полной версией

**AI-обогащение (`enrichAsync`):**
- Выполняется асинхронно (не блокирует сохранение)
- Таймаут: 60 секунд
- При ошибке — только лог, статья уже сохранена
- Если `openrouter == nil` (ключ не задан) — no-op

### 4.5. similarity (`internal/similarity/similarity.go`)

Чисто-Go реализация косинусного сходства для новостной дедупликации. Без внешних зависимостей.

**Алгоритм:**
1. Токенизация: приведение к нижнему регистру, разбивка по не-буквенным символам
2. Фильтрация: токены < 3 символов и стоп-слова (рус + eng) удаляются
3. TF-вектор: частота каждого токена / общее кол-во токенов
4. Косинусное сходство: `dot(a,b) / (|a| * |b|)`

**Порог:** `Threshold = 0.55` — статьи считаются дублями при сходстве ≥ 55%.

Стоп-слова покрывают русский и английский языки (~120 слов).

### 4.6. ai/openrouter (`internal/ai/openrouter.go`)

**Технология:** HTTP-клиент к OpenRouter (OpenAI-совместимый API).

**Метод `EnrichArticle(ctx, title, content)`:**
- Если суммарная длина `title+content` < 80 рун — возвращает пустые строки (нет смысла обогащать)
- Обрезает `content` до 5000 рун перед отправкой
- Отправляет промпт редактора с двумя заданиями: заголовок и краткое изложение
- Парсит ответ по маркерам `ЗАГОЛОВОК:` и `КРАТКОЕ:`

**Промпт:**
- Заголовок: 6–12 слов, конкретные имена/цифры, активный залог, без вводных фраз
- Изложение: 2–3 предложения, структура "кто + что + где/когда + результат", 60–150 слов

**Таймаут HTTP-клиента:** 90 секунд.

**Модель по умолчанию:** `qwen/qwen-2.5-72b-instruct:free` (бесплатная).

### 4.7. ai/gemini (`internal/ai/gemini.go`)

**Технология:** HTTP-клиент к Google Gemini API.

Используется только ботом для анализа медиафайлов.

**Методы:**

| Метод | Описание |
|---|---|
| `AnalyzeYouTube(ctx, url)` | Передаёт YouTube URL как `fileData` напрямую |
| `AnalyzeImage(ctx, data, mimeType)` | Передаёт изображение как base64 inline |
| `AnalyzeVideo(ctx, data, filename)` | Загружает видео в Gemini Files API, ждёт ACTIVE, анализирует |
| `SaveToObsidian(content, sourceURL, mediaType)` | Сохраняет результат как `.md` в vault/inbox/ |

**Загрузка видео (Files API):**
1. Multipart POST с метаданными + файловыми байтами
2. Polling каждые 3 сек до статуса ACTIVE (макс 30 попыток = 90 сек)
3. После ACTIVE — отправка запроса на анализ

**Сохранение в Obsidian (`SaveToObsidian`):**
- Создаёт `{vault}/inbox/` если не существует
- Имя файла: `{date}-{slug}.md` (slug — транслитерированный заголовок)
- YAML frontmatter: `tags`, `date`, `source`, `type`, `created`
- Теги извлекаются из раздела `## Теги` ответа Gemini, потом этот раздел удаляется из тела
- Если файл уже существует — добавляет timestamp к имени

**Промпты:**
- Видео: структурированный конспект (суть, ключевые мысли, лайфхаки, факты, инструменты, выводы, теги)
- Изображение: извлечение информации из скриншота/инфографики/фото

**Таймаут HTTP-клиента:** 180 секунд.

**Модель по умолчанию:** `gemini-2.0-flash`.

### 4.8. api (`internal/api/handler.go`)

**Технология:** net/http (Go 1.22 routing patterns).

**Эндпоинты:**

```
GET  /api/health              — "ok" (для health check)
GET  /api/news                — список статей с cursor-пагинацией и фильтрацией
GET  /api/news/{id}           — полная статья по ID
GET  /api/news/{id}/summary   — краткая сводка: ai_title, summary, tags
POST /api/news/{id}/rate      — оценить статью (хорошая/плохая)
POST /api/news/{id}/save      — сохранить статью в Obsidian через Gemini
```

**Параметры GET /api/news:**

| Параметр | Тип | По умолчанию | Описание |
|---|---|---|---|
| `limit` | int | 20 | Кол-во статей (max 100) |
| `after` | int64 | — | ID статьи; вернуть более старые (id < after). Для скролла вниз |
| `since` | int64 | — | ID статьи; вернуть более новые (id > since). Для live-обновлений |
| `tags` | string | — | Фильтр по тегам через запятую. OR-семантика: `tags=россия,санкции` |

**Пример ответа GET /api/news:**

```json
{
  "articles": [
    {
      "id": 42,
      "source_id": 3,
      "title": "Оригинальный заголовок из источника",
      "ai_title": "Конкретный AI-заголовок с именами и фактами",
      "summary": "Краткое изложение в 2–3 предложениях от AI.",
      "tags": ["россия", "санкции", "экономика"],
      "content": "Полный текст...",
      "url": "https://t.me/channel/123",
      "image_url": "http://localhost:9000/news-images/images/3/123.jpg",
      "source": "channel",
      "likes": 5,
      "dislikes": 1,
      "pub_date": "2025-03-05T12:00:00Z",
      "created_at": "2025-03-05T12:05:00Z"
    }
  ],
  "has_more": true
}
```

**GET /api/news/{id}/summary** — облегчённый ответ без полного текста:
```json
{
  "id": 42,
  "title": "Оригинальный заголовок",
  "ai_title": "AI-заголовок",
  "summary": "Краткое изложение...",
  "tags": ["россия", "санкции"]
}
```

**POST /api/news/{id}/rate** — тело запроса:
```json
{"vote": "good"}   // или "bad"
```
Ответ: `{"likes": 6, "dislikes": 1}`

**POST /api/news/{id}/save** — сохраняет статью в Obsidian через Gemini. Требует `GEMINI_API_KEY` и `OBSIDIAN_VAULT_PATH` у API-сервиса. Ответ: `{"file": "inbox/2026-03-05-article-slug.md"}`

Если Gemini не настроен — 503 Service Unavailable.

**Определение has_more:** запрашивается `limit+1` записей. Если получено больше `limit` — `has_more=true`, лишняя запись отбрасывается.

**CORS:** разрешены все origin (`*`), GET, POST и OPTIONS.

### 4.9. s3 (`internal/s3/s3.go`)

**Технология:** minio-go v7.

- При инициализации проверяет существование бакета, создаёт если нет
- Устанавливает public-read политику на бакет (GetObject разрешен для `*`)
- Метод `Upload(ctx, key, data, contentType)` -> загрузка + возврат публичного URL
- URL формат: `{S3_PUBLIC_URL}/{bucket}/{key}`

### 4.10. notify (`internal/notify/notify.go`)

Минимальный пакет для отправки уведомлений через Bot API без зависимости от пакета `bot`.

- Прямой HTTP POST к `https://api.telegram.org/bot{token}/sendMessage`
- Тайм-аут 10 секунд
- Если `botToken` пустой или `chatID=0` — ничего не делает (silent no-op)

Используется и парсером, и юзерботом для отправки уведомлений о новых статьях.

---

## 5. Переменные окружения

| Переменная | Обязательна | Сервисы | Описание |
|---|---|---|---|
| `TELEGRAM_TOKEN` | Да | bot, parser, userbot | Токен от @BotFather |
| `DATABASE_URL` | Да | все 4 | PostgreSQL connection string |
| `NOTIFY_CHAT_ID` | Нет | parser, userbot | Chat ID для уведомлений о новых статьях |
| `PARSE_INTERVAL_MIN` | Нет (=15) | parser, userbot | Интервал обхода в минутах |
| `TG_API_ID` | Да (userbot) | userbot | Telegram API ID с my.telegram.org |
| `TG_API_HASH` | Да (userbot) | userbot | Telegram API Hash |
| `TG_PHONE` | Нет | userbot | Номер телефона (можно ввести интерактивно) |
| `TG_2FA_PASSWORD` | Нет | userbot | Пароль 2FA |
| `TG_SESSION_PATH` | Нет (=/data/userbot.session) | userbot | Путь к файлу сессии |
| `S3_ENDPOINT` | Нет (=minio:9000) | userbot | Эндпоинт MinIO |
| `S3_ACCESS_KEY` | Нет (=minioadmin) | userbot | Логин MinIO |
| `S3_SECRET_KEY` | Нет (=minioadmin) | userbot | Пароль MinIO |
| `S3_BUCKET` | Нет (=news-images) | userbot | Название бакета |
| `S3_PUBLIC_URL` | Нет (=http://localhost:9000) | userbot | Публичный URL для ссылок в API |
| `API_ADDR` | Нет (=:8080) | api | Адрес HTTP-сервера |
| `API_PORT` | Нет (=8080) | docker-compose | Внешний порт API |
| `POSTGRES_PASSWORD` | Нет (=rssreader) | docker-compose | Пароль PostgreSQL |
| `OPENROUTER_API_KEY` | Нет | parser, userbot | Ключ OpenRouter для AI-обогащения статей |
| `OPENROUTER_MODEL` | Нет (=qwen/qwen-2.5-72b-instruct:free) | parser, userbot | Модель OpenRouter |
| `GEMINI_API_KEY` | Нет | bot | Ключ Google Gemini для анализа медиа |
| `GEMINI_MODEL` | Нет (=gemini-2.0-flash) | bot | Модель Gemini |
| `OBSIDIAN_VAULT_HOST_PATH` | Нет | docker-compose | Путь к vault на хост-машине (монтируется в бот) |
| `OBSIDIAN_VAULT_PATH` | Нет | bot (внутри контейнера) | Путь к vault внутри контейнера (устанавливается docker-compose) |

---

## 6. Что видит пользователь — функции бота

### 6.1. Управление источниками

**Добавить RSS-ленту:**
```
/add https://habr.com/ru/rss/best/daily/ Хабр
```
Бот ответит "✅ RSS-источник добавлен". При следующем цикле парсера статьи начнут накапливаться.

**Добавить Telegram-канал:**
```
/addchannel @durov
/addchannel https://t.me/durov
/addchannel https://t.me/joinchat/XXXXXX  (приватный)
```
Бот нормализует ссылку и сохраняет. Юзербот подпишется и начнёт читать при следующем обходе.

**Посмотреть все источники:**
```
/list
```
Ответ:
```
📋 Источники:

[1] [RSS] Хабр
https://habr.com/ru/rss/best/daily/

[2] [TG] durov
durov
```

**Удалить источник:**
```
/remove 2
```
Все статьи из этого источника тоже удалятся (CASCADE).

### 6.2. Чтение новостей

**Последние 10 статей прямо в боте:**
```
/news
```
Показывает список ссылок. Если AI успел обогатить — показывает AI-заголовок.

**Через HTTP API** (для фронтенда или клиента):
```bash
# Последние 20 статей
curl http://localhost:8080/api/news

# С пагинацией (подгрузить старее)
curl "http://localhost:8080/api/news?after=42&limit=10"

# Обновления с последнего визита
curl "http://localhost:8080/api/news?since=100"

# Только статьи по тегам (OR-семантика)
curl "http://localhost:8080/api/news?tags=россия,санкции"

# Полная статья по ID
curl http://localhost:8080/api/news/42

# Краткая сводка (ai_title + summary + tags, без полного текста)
curl http://localhost:8080/api/news/42/summary

# Оценить статью
curl -X POST http://localhost:8080/api/news/42/rate \
     -H "Content-Type: application/json" \
     -d '{"vote":"good"}'
# Ответ: {"likes":6,"dislikes":1}

# Сохранить статью в Obsidian (требует GEMINI_API_KEY у api-сервиса)
curl -X POST http://localhost:8080/api/news/42/save
# Ответ: {"file":"inbox/2026-03-05-russia-sanctions.md"}
```

### 6.3. AI-функции в боте (только при `GEMINI_API_KEY`)

**Анализ YouTube-видео** (любым из способов):
```
/analyze https://youtube.com/watch?v=dQw4w9WgXcQ
```
или просто вставить ссылку в чат без команды.

**Анализ изображения или скриншота:** просто отправить фото.

**Анализ видеофайла:** отправить файл (.mp4, .mov, .avi, .mkv, .webm).

Бот отвечает:
```
✅ Сохранено: inbox/2026-03-05-rick-astley-never-gonna-give.md

# Rick Astley — Never Gonna Give You Up

## Суть
...
```

---

## 7. Известные ограничения и технический долг

### 7.1. Критичные

1. **Нет разграничения пользователей.** Все пользователи бота работают с одним пулом источников. Если бот публичный, любой может добавлять/удалять источники. Поле `chat_id` в `sources` записывается, но нигде не используется для фильтрации.

2. **Нет graceful shutdown у бота.** Сервис `bot` использует `GetUpdatesChan` без контекста — не реагирует на SIGTERM. Parser и userbot корректно обрабатывают сигналы.

3. **Парсинг источников последовательный.** И parser, и userbot обходят источники один за другим. При большом количестве источников обход может не уложиться в интервал.

4. **Нет тестов.** Ни unit, ни интеграционных.

5. **AI-обогащение без retry.** Если OpenRouter вернул ошибку, статья остаётся без AI-полей навсегда — повторного запроса нет.

### 7.2. Средние

6. **Миграции без версионирования.** Используется `CREATE TABLE IF NOT EXISTS` — работает только для первоначального создания. Любое изменение схемы требует ручного ALTER TABLE или пересоздания БД. (Исключение: колонки `ai_title`/`summary` добавляются через `ADD COLUMN IF NOT EXISTS`.)

7. **Нет rate limiting для юзербота.** Telegram может заблокировать аккаунт при слишком частых запросах. Нет обработки FloodWait ошибок.

8. **API без аутентификации.** Любой может читать все новости. Нет API ключей, JWT, или basic auth.

9. **MinIO URL хардкодится при записи.** Если `S3_PUBLIC_URL` изменится, старые ссылки на картинки станут нерабочими.

10. **Метод `ServeHTTP` в api/handler.go создает новый mux на каждый запрос.** Это не используется (используется `NewMux()`), но является мертвым кодом.

11. **Юзербот не обрабатывает видео, документы, голосовые.** Только фото из `MessageMediaPhoto`.

### 7.3. Минорные

12. **Дублирование хелпер-функций** `mustEnv`, `getenv`, `getenvInt`, `getenvInt64` в каждом `cmd/*/main.go`.

13. **Markdown escaping неполный.** Функция `escapeMarkdown` не экранирует все спец. символы Markdown V1.

14. **Индекс `idx_articles_created_at` назван неточно** — он на самом деле по полю `id DESC`, а не по `created_at`.

15. **RSS-парсер не извлекает картинки из enclosure.** Некоторые RSS-фиды хранят картинки в `<enclosure>`, а не в `<image>`.

16. **AI-обогащение без ограничения параллельности.** Каждая новая статья запускает горутину. При большом пакете статей может одновременно лететь много запросов к OpenRouter.

---

## 8. Идеи для развития

### 8.1. Приоритет 1 — Необходимо для продакшена

#### 8.1.1. Мультитенантность (разграничение пользователей)

**Суть:** Каждый пользователь бота видит только свои источники и статьи.

**Как реализовать:**
- В `sources` уже есть поле `chat_id`. Нужно добавить фильтрацию `WHERE chat_id = $1` в `ListSources`, `ListSourcesByType`, `RemoveSource`
- В `articles` добавить фильтрацию через JOIN с sources: `WHERE s.chat_id = $1`
- В API: либо добавить query-параметр `chat_id`, либо авторизацию (JWT/API key привязанный к chat_id)
- В `bot.go`: передавать `msg.Chat.ID` во все запросы к БД

**Файлы для изменения:** `internal/db/db.go` (все методы — добавить параметр chatID), `internal/bot/bot.go` (пробрасывать chatID), `internal/api/handler.go` (фильтрация)

#### 8.1.2. Graceful shutdown бота

**Как:** Заменить `GetUpdatesChan` на `GetUpdates` с контекстом, или использовать webhook.

**Файлы:** `internal/bot/bot.go` (метод `Run`), `cmd/bot/main.go` (добавить signal handling как в parser)

#### 8.1.3. Параллельный парсинг

**Как:** Использовать `errgroup` с лимитом concurrency (например, `semaphore.NewWeighted(5)`).

```go
g, ctx := errgroup.WithContext(ctx)
g.SetLimit(5) // макс 5 параллельных фидов
for _, src := range sources {
    src := src
    g.Go(func() error { ... })
}
g.Wait()
```

**Файлы:** `internal/parser/parser.go` (функция `parse`), `internal/userbot/userbot.go` (функция `poll`)

#### 8.1.4. FloodWait для юзербота

**Как:** Использовать middleware из `github.com/gotd/contrib/middleware/floodwait`:

```go
waiter := floodwait.NewSimpleWaiter()
client := telegram.NewClient(apiID, apiHash, telegram.Options{
    Middlewares: []telegram.Middleware{waiter},
})
```

**Файлы:** `internal/userbot/userbot.go` (метод `Run`, создание клиента)

#### 8.1.5. Retry для AI-обогащения

**Как:** В `enrichAsync` добавить простой backoff-retry (3 попытки с задержкой). Или хранить флаг `ai_enriched` в БД и запускать отдельный worker для повторной обработки необогащённых статей.

### 8.2. Приоритет 2 — Качество и удобство

#### 8.2.1. Миграции с версионированием

**Варианты:**
- `github.com/golang-migrate/migrate` — файловые миграции (SQL-файлы в `migrations/`)
- `github.com/pressly/goose` — альтернатива

#### 8.2.2. Конфигурация через общий пакет

Вынести `mustEnv`, `getenv` и т.д. в `internal/config/config.go`.

#### 8.2.3. API — фильтрация и поиск

Добавить query-параметры:
- `source_id=3` — статьи из конкретного источника
- `source_type=telegram` — только из Telegram
- `q=поисковый запрос` — полнотекстовый поиск (PostgreSQL `to_tsvector` / `ts_query`)

#### 8.2.4. Webhook вместо polling для бота

Требует домен с SSL (или nginx reverse proxy).

#### 8.2.5. Обработка видео и документов в юзерботе

Сейчас обрабатываются только фото (`MessageMediaPhoto`). Расширить на `MessageMediaDocument`.

### 8.3. Приоритет 3 — Новые фичи

#### 8.3.1. WebSocket / SSE для real-time обновлений

Server-Sent Events проще чем WebSocket. Parser/userbot публикуют в PostgreSQL LISTEN/NOTIFY, API слушает и рассылает клиентам.

#### 8.3.2. Веб-интерфейс

SPA (React/Vue/Svelte) с бесконечным скроллом. API уже поддерживает cursor-пагинацию и поля `ai_title`/`summary` для отображения.

#### 8.3.3. Мониторинг

- Prometheus метрики
- Healthcheck с проверкой БД (сейчас только "ok")
- Структурированные JSON-логи через `slog`

#### 8.3.4. Авторизация через Telegram Login Widget

Для веб-интерфейса без паролей. Связывается с мультитенантностью.

#### 8.3.5. Экспорт

- RSS/Atom фид из агрегированных новостей
- Email-дайджест

---

## 9. Справочник по файлам

| Файл | Что делает |
|---|---|
| `cmd/bot/main.go` | Точка входа бота. БД, Gemini-клиент (опционально), запуск |
| `cmd/parser/main.go` | Точка входа парсера. БД, OpenRouter-клиент (опционально), таймер, signal handling |
| `cmd/userbot/main.go` | Точка входа юзербота. БД, S3, OpenRouter-клиент (опционально), MTProto клиент, signal handling |
| `cmd/api/main.go` | Точка входа API. БД, HTTP сервер |
| `internal/db/models.go` | Типы: Source, Article (+ Summary, AITitle), ArticleLite, SourceType |
| `internal/db/db.go` | PostgreSQL: подключение, миграции, CRUD, пагинация, AI-поля |
| `internal/bot/bot.go` | Telegram-бот: команды, Gemini-медиаанализ, нормализация ссылок |
| `internal/parser/parser.go` | RSS-парсер: gofeed, таймер, извлечение контента |
| `internal/userbot/userbot.go` | MTProto: авторизация, каналы, история, скачивание фото |
| `internal/processor/processor.go` | Дедупликация + AI-обогащение: общий pipeline для parser и userbot |
| `internal/similarity/similarity.go` | Косинусное сходство на TF-векторах. Порог 0.55 |
| `internal/ai/openrouter.go` | OpenRouter client: EnrichArticle (заголовок + краткое изложение) |
| `internal/ai/gemini.go` | Gemini client: AnalyzeYouTube/Image/Video + SaveToObsidian |
| `internal/s3/s3.go` | MinIO: создание бакета, загрузка, публичный URL |
| `internal/api/handler.go` | HTTP API: cursor-пагинация, CORS, JSON (включая ai_title, summary) |
| `internal/notify/notify.go` | Уведомления: HTTP POST к Bot API |
| `Dockerfile` | Multi-stage build с параметром SERVICE |
| `docker-compose.yml` | 6 сервисов: postgres, minio, bot, parser, userbot, api |
| `.env.example` | Все переменные окружения с комментариями |
| `go.mod` | Зависимости: pgx, gotd/td, gofeed, minio-go, telegram-bot-api |

---

## 10. Типичные сценарии работы с кодом

### Добавить новую команду в бота

1. Открыть `internal/bot/bot.go`
2. Добавить case в `switch cmd` в функции `handle` (~строка 89)
3. Написать метод `cmdXxx(chatID int64, args []string)`
4. Обновить `buildHelpText()` (функция в конце файла)

### Добавить новый тип источника

1. Добавить константу в `internal/db/models.go` (e.g. `SourceYouTube`)
2. Создать пакет `internal/youtube/` с парсером, использующим `processor.Process()`
3. Создать `cmd/youtube/main.go` — аналогично cmd/parser (с OpenRouter и processor)
4. Добавить сервис в `docker-compose.yml`
5. Добавить команду в бота (`/addyoutube`)

### Изменить схему БД

1. Изменить SQL в функции `migrate` (`internal/db/db.go`)
   - Для новых колонок: добавить `ALTER TABLE ... ADD COLUMN IF NOT EXISTS` во вторую часть migrate
   - Для новых таблиц: `CREATE TABLE IF NOT EXISTS` в первую часть
2. Обновить соответствующие структуры в `internal/db/models.go`
3. Обновить Scan-вызовы в методах чтения

### Добавить новый эндпоинт API

1. Открыть `internal/api/handler.go`
2. Добавить маршрут в `NewMux()` (~строка 48)
3. Написать handler-метод на `*Handler`
4. При необходимости — добавить метод в `internal/db/db.go`

### Изменить AI-промпт

- **OpenRouter (обогащение новостей):** `internal/ai/openrouter.go`, функция `buildEnrichPrompt`
- **Gemini видео:** `internal/ai/gemini.go`, константа `videoAnalysisPrompt`
- **Gemini изображения:** `internal/ai/gemini.go`, константа `imageAnalysisPrompt`

---

## 11. Запуск и отладка

### Первый запуск

```bash
# 1. Скопировать и заполнить конфиг
cp .env.example .env
# Обязательно: TELEGRAM_TOKEN, TG_API_ID, TG_API_HASH
# Опционально: OPENROUTER_API_KEY (AI-обогащение), GEMINI_API_KEY + OBSIDIAN_VAULT_HOST_PATH (медиаанализ)

# 2. Поднять инфраструктуру
docker compose up -d postgres minio

# 3. Поднять сервисы (кроме юзербота)
docker compose up -d bot parser api

# 4. Первый запуск юзербота (интерактивно)
docker compose run -it userbot
# Ввести номер телефона, код из Telegram, (2FA пароль)
# После успешной авторизации — Ctrl+C

# 5. Запустить юзербот в фоне
docker compose up -d userbot
```

### Проверка API

```bash
# Последние 5 новостей
curl http://localhost:8080/api/news?limit=5

# Новости старше id=100
curl "http://localhost:8080/api/news?after=100&limit=10"

# Новые новости с момента id=50
curl "http://localhost:8080/api/news?since=50"
```

### Логи

```bash
docker compose logs -f bot        # логи бота (+ Gemini-анализ)
docker compose logs -f parser     # логи RSS-парсера (+ OpenRouter)
docker compose logs -f userbot    # логи юзербота (+ OpenRouter)
docker compose logs -f api        # логи API
```

### Пересборка одного сервиса

```bash
docker compose build parser
docker compose up -d parser
```

### Локальная разработка (без Docker)

```bash
export DATABASE_URL="postgres://user:pass@localhost:5432/rssreader?sslmode=disable"
export TELEGRAM_TOKEN="..."
export OPENROUTER_API_KEY="..."   # опционально
export GEMINI_API_KEY="..."       # опционально
export OBSIDIAN_VAULT_PATH="/path/to/vault"  # опционально

go run ./cmd/bot
go run ./cmd/parser
go run ./cmd/api
```

### Проверка сборки всех сервисов

```bash
go build ./cmd/bot && go build ./cmd/parser && go build ./cmd/api && go build ./cmd/userbot
```
