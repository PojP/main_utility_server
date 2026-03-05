# Развертывание на сервер

## 🖥️ Требования

- Docker & Docker Compose (v2.0+)
- 2GB RAM минимум
- 10GB дискового пространства (для БД и картинок)
- Открытые порты: 8080 (API), 5432 (PostgreSQL internal), 9000 (MinIO internal)

## 📋 Способ 1: Docker Compose (Рекомендуется)

### 1. Подготовка сервера

```bash
# SSH на сервер
ssh user@your_server.com

# Установить Docker (Ubuntu/Debian)
sudo apt update && sudo apt install -y docker.io docker-compose-plugin
sudo usermod -aG docker $USER
newgrp docker

# Или для других систем см: https://docs.docker.com/engine/install/
```

### 2. Клонировать репозиторий

```bash
cd /opt
git clone https://github.com/PojP/main_utility_server.git
cd main_utility_server
```

### 3. Настроить .env файл

```bash
cp .env.example .env
nano .env
```

**Важные переменные:**

```env
# Database
POSTGRES_USER=postgres
POSTGRES_PASSWORD=your_secure_password_here
POSTGRES_DB=rss_reader

# MinIO (хранилище картинок)
MINIO_ROOT_USER=minioadmin
MINIO_ROOT_PASSWORD=your_secure_password_here
MINIO_BUCKET_NAME=rss-reader

# Telegram Bot
TELEGRAM_BOT_TOKEN=your_bot_token_from_botfather

# Telegram Userbot (для чтения каналов)
TELEGRAM_API_ID=your_api_id
TELEGRAM_API_HASH=your_api_hash
TELEGRAM_PHONE=your_phone_number

# API
API_PORT=8080
API_HOST=0.0.0.0

# Опционально: AI сервисы
GEMINI_API_KEY=your_key_if_using_gemini
OPENROUTER_API_KEY=your_key_if_using_openrouter
```

**Где получить ключи:**
- `TELEGRAM_BOT_TOKEN`: [@BotFather](https://t.me/botfather)
- `TELEGRAM_API_ID`, `TELEGRAM_API_HASH`: https://my.telegram.org
- `GEMINI_API_KEY`: https://makersuite.google.com/app/apikey
- `OPENROUTER_API_KEY`: https://openrouter.ai

### 4. Запустить стек

```bash
# Используя образы из GHCR (GitHub Container Registry)
docker compose pull
docker compose up -d

# Или собрать локально
docker compose up -d --build
```

### 5. Первый запуск Userbot (интерактивно)

Userbot нужно авторизовать по MTProto один раз:

```bash
# Интерактивная аутентификация
docker compose run -it userbot

# После ввода пароля и кода - Ctrl+C и запустить нормально
docker compose up userbot -d
```

### 6. Проверить статус

```bash
# Все контейнеры должны быть running
docker compose ps

# Смотреть логи
docker compose logs -f api     # API логи
docker compose logs -f bot     # Bot логи
docker compose logs -f parser  # Parser логи
docker compose logs -f userbot # Userbot логи

# Проверить API
curl http://localhost:8080/api/news?limit=5
```

## 🐳 Способ 2: Использовать только Docker образы из GHCR

Если уже есть PostgreSQL и MinIO на сервере:

```bash
# Залогиниться в GHCR
echo $GITHUB_TOKEN | docker login ghcr.io -u $GITHUB_USERNAME --password-stdin

# Запустить отдельно
docker run -d \
  --name rss-bot \
  -e TELEGRAM_BOT_TOKEN=your_token \
  -e DATABASE_URL=postgres://user:pass@host:5432/db \
  ghcr.io/pojp/rss-reader:main-bot

docker run -d \
  --name rss-parser \
  -e DATABASE_URL=postgres://user:pass@host:5432/db \
  ghcr.io/pojp/rss-reader:main-parser

# и т.д.
```

## 🔒 Security Best Practices

### 1. Nginx Reverse Proxy (рекомендуется)

```bash
# Установить Nginx
sudo apt install -y nginx

# Создать конфиг
sudo nano /etc/nginx/sites-available/rss-reader

```

**Содержимое:**

```nginx
server {
    listen 80;
    server_name your_domain.com;

    location / {
        proxy_pass http://localhost:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}
```

```bash
# Включить конфиг
sudo ln -s /etc/nginx/sites-available/rss-reader /etc/nginx/sites-enabled/
sudo nginx -t && sudo systemctl restart nginx

# SSL с Let's Encrypt
sudo apt install -y certbot python3-certbot-nginx
sudo certbot --nginx -d your_domain.com
```

### 2. Firewall

```bash
sudo ufw enable
sudo ufw allow 22/tcp   # SSH
sudo ufw allow 80/tcp   # HTTP
sudo ufw allow 443/tcp  # HTTPS
sudo ufw deny 5432/tcp  # PostgreSQL (только внутри)
sudo ufw deny 9000/tcp  # MinIO (только внутри)
```

### 3. Резервные копии

```bash
# Скрипт ежедневного бэкапа БД
cat > /opt/backup.sh << 'EOF'
#!/bin/bash
DATE=$(date +%Y%m%d_%H%M%S)
BACKUP_DIR=/opt/backups
mkdir -p $BACKUP_DIR

# PostgreSQL dump
docker compose exec -T postgres pg_dump -U postgres rss_reader | \
  gzip > $BACKUP_DIR/db_$DATE.sql.gz

# MinIO sync
docker compose exec -T minio mc mirror \
  /data $BACKUP_DIR/minio_$DATE --overwrite

# Удалить старые бэкапы (старше 7 дней)
find $BACKUP_DIR -type f -mtime +7 -delete
EOF

chmod +x /opt/backup.sh

# Добавить в crontab
(crontab -l 2>/dev/null; echo "0 3 * * * /opt/backup.sh") | crontab -
```

## 🔄 Обновление

### Обновить код и образы

```bash
cd /opt/main_utility_server

# Получить обновления
git pull origin main

# Обновить образы
docker compose pull

# Перезапустить (с нулевым даунтаймом)
docker compose up -d
```

### Zero-downtime deployment

```bash
# Остановить только изменившиеся сервисы
docker compose up -d bot parser userbot api

# или переключить отдельно
docker compose up -d api  # Дождаться пока будет ready
```

## 📊 Мониторинг

### Проверить статус сервисов

```bash
# Функция в bash_profile
rss-status() {
  docker compose -f /opt/main_utility_server/docker-compose.yml ps
}

rss-logs() {
  docker compose -f /opt/main_utility_server/docker-compose.yml logs -f ${1:-api}
}

rss-restart() {
  docker compose -f /opt/main_utility_server/docker-compose.yml restart ${1}
}
```

### Использование

```bash
rss-status              # Показать статус
rss-logs api            # Логи API
rss-logs parser         # Логи парсера
rss-restart bot         # Перезапустить бота
```

## 🐛 Troubleshooting

### Ошибка: "Cannot connect to PostgreSQL"

```bash
# Проверить что контейнер postgres запущен
docker compose ps postgres

# Проверить логи
docker compose logs postgres

# Перезапустить
docker compose restart postgres
```

### Ошибка: "Userbot needs session"

```bash
# Требуется интерактивная аутентификация
docker compose run -it userbot
# Ввести номер телефона, пароль, 2FA код
```

### Высокое использование памяти

```bash
# Смотреть ресурсы контейнеров
docker stats

# Лимитировать память в docker-compose.yml
services:
  api:
    mem_limit: 512m
  parser:
    mem_limit: 256m
```

## 📞 Получить помощь

- Логи: `docker compose logs service_name`
- Статус: `docker compose ps`
- Перестройка: `docker compose up -d --build`
- Очистка: `docker compose down -v` (удалит все данные!)

## Переменные окружения по умолчанию

Полный список в `.env.example`. Основные:

```env
# Postgres
POSTGRES_USER=postgres
POSTGRES_PASSWORD=postgres
POSTGRES_DB=rss_reader

# API
API_PORT=8080
API_HOST=0.0.0.0

# Telegram
TELEGRAM_BOT_TOKEN=required
TELEGRAM_API_ID=required
TELEGRAM_API_HASH=required
TELEGRAM_PHONE=required

# MinIO
MINIO_ROOT_USER=minioadmin
MINIO_ROOT_PASSWORD=minioadmin
MINIO_BUCKET_NAME=rss-reader
```
