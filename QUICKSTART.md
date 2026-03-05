# 🚀 Быстрый старт развертывания

## За 5 минут на сервер

### Способ 1️⃣: Автоматический скрипт (самый простой)

```bash
# SSH на сервер с правами sudo
ssh user@your_server.com

# Скачать и запустить скрипт развертывания
curl -fsSL https://raw.githubusercontent.com/PojP/main_utility_server/main/scripts/deploy.sh | sudo bash
```

Скрипт:
- ✅ Установит Docker
- ✅ Клонирует репозиторий
- ✅ Создаст .env файл (требует редактирования)
- ✅ Запустит все сервисы
- ✅ Настроит Userbot (интерактивно)

---

### Способ 2️⃣: Ручное развертывание

```bash
# 1. Подключиться к серверу
ssh user@your_server.com

# 2. Установить Docker (если не установлен)
sudo apt update && sudo apt install -y docker.io docker-compose-plugin git
sudo usermod -aG docker $USER

# 3. Перелогиниться для применения прав
exec su - $USER

# 4. Клонировать репозиторий
cd /opt
git clone https://github.com/PojP/main_utility_server.git
cd main_utility_server

# 5. Скопировать и заполнить конфиг
cp .env.example .env
nano .env  # Отредактировать переменные окружения

# 6. Запустить сервисы
docker compose -f docker-compose.yml -f docker-compose.prod.yml up -d

# 7. Настроить Userbot (интерактивно)
docker compose run -it userbot
# Ввести номер телефона, пароль, 2FA код...
# Ctrl+C чтобы выйти

# 8. Запустить Userbot как демон
docker compose up userbot -d
```

---

## 📋 Требуемые параметры в .env

```env
# 🔐 ОБЯЗАТЕЛЬНО заполнить:
POSTGRES_PASSWORD=your_secure_password_123
S3_ACCESS_KEY=your_minio_user
S3_SECRET_KEY=your_minio_password

TELEGRAM_TOKEN=123456:ABC-DEF1234ghIkl-zyx57W2v1u123ew11

TELEGRAM_API_ID=12345678
TELEGRAM_API_HASH=abcdef1234567890abcdef1234567890
TELEGRAM_PHONE=+79991234567

# 🟦 Опционально (для AI функций):
GEMINI_API_KEY=
OPENROUTER_API_KEY=

# 📬 Для уведомлений (опционально):
NOTIFY_CHAT_ID=
```

**Где получить:**
- **TELEGRAM_TOKEN**: Напишите [@BotFather](https://t.me/botfather) → `/newbot`
- **TELEGRAM_API_ID/HASH**: Зайдите на https://my.telegram.org → API development tools
- **TELEGRAM_PHONE**: Ваш номер телефона (вида +7xxxxxxxxxx)

---

## ✅ Проверка после развертывания

```bash
# Перейти в папку проекта
cd /opt/main_utility_server

# Проверить статус всех сервисов
docker compose ps

# Все должны быть ✅ Running

# Проверить API
curl http://localhost:8080/api/news?limit=1

# Смотреть логи
docker compose logs -f api

# Статус бота
docker compose logs -f bot
```

---

## 🌐 Доступ из интернета (ОБЯЗАТЕЛЬНО для production)

По умолчанию API доступен только локально. Для доступа из интернета нужен Nginx:

```bash
# 1. Установить Nginx
sudo apt install -y nginx

# 2. Создать конфиг
sudo nano /etc/nginx/sites-available/rss-reader
```

**Содержимое:**
```nginx
upstream api {
    server localhost:8080;
}

server {
    listen 80;
    server_name your_domain.com;

    location / {
        proxy_pass http://api;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_buffering off;
        proxy_request_buffering off;
    }
}
```

```bash
# 3. Включить конфиг
sudo ln -s /etc/nginx/sites-available/rss-reader /etc/nginx/sites-enabled/
sudo nginx -t
sudo systemctl restart nginx

# 4. Включить HTTPS (Let's Encrypt)
sudo apt install -y certbot python3-certbot-nginx
sudo certbot --nginx -d your_domain.com
```

---

## 🔒 Firewall (важно для безопасности!)

```bash
# Включить UFW
sudo ufw enable

# Разрешить необходимые порты
sudo ufw allow 22/tcp    # SSH
sudo ufw allow 80/tcp    # HTTP
sudo ufw allow 443/tcp   # HTTPS

# Блокировать остальное (БД и MinIO доступны только локально)
sudo ufw deny 5432/tcp   # PostgreSQL
sudo ufw deny 9000/tcp   # MinIO
```

---

## 📊 Полезные команды

```bash
cd /opt/main_utility_server

# Статус сервисов
docker compose ps

# Логи конкретного сервиса
docker compose logs -f api      # API
docker compose logs -f bot      # Бот
docker compose logs -f parser   # Парсер новостей

# Перезапустить сервис
docker compose restart api

# Остановить все
docker compose down

# Остановить и удалить все данные (!)
docker compose down -v
```

---

## 🔄 Обновление

```bash
cd /opt/main_utility_server

# Получить новый код
git pull origin main

# Обновить образы
docker compose -f docker-compose.yml -f docker-compose.prod.yml pull

# Перезапустить сервисы
docker compose -f docker-compose.yml -f docker-compose.prod.yml up -d
```

---

## 🆘 Если что-то сломалось

```bash
# 1. Проверить логи
docker compose logs api | tail -50

# 2. Перезапустить сервис
docker compose restart api

# 3. Если база потеряла соединение
docker compose restart postgres

# 4. Переубедить всё (опасно - потеряет данные!)
docker compose down -v
docker compose -f docker-compose.yml -f docker-compose.prod.yml up -d
```

---

## 📞 Где искать помощь

- **Логи**: `docker compose logs -f service_name`
- **Docs**: `docs/DEPLOYMENT.md` в репозитории
- **GitHub Issues**: https://github.com/PojP/main_utility_server/issues
- **Telegram**: Написать в чат поддержки

---

**Готово! 🎉** Ваш сервер должен быть запущен и работать.
