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

# 5. Создать .env (выберите способ)

# Способ A: Вручную
cp .env.example .env
nano .env  # Отредактировать переменные окружения

# Способ B: Из переменных окружения (безопаснее для автоматизации)
export TELEGRAM_TOKEN="your_token"
export POSTGRES_PASSWORD="your_password"
export TG_API_ID="1710401"
export TG_API_HASH="your_hash"
export TG_PHONE="+7999..."
bash scripts/setup-env.sh

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

## ⚙️ Если Docker Compose не устанавливается

На Debian 12 и некоторых других системах `docker-compose-plugin` может быть недоступен. Используйте универсальный скрипт:

```bash
# Автоматическая установка Docker и Compose (V1 или V2)
curl -fsSL https://raw.githubusercontent.com/PojP/main_utility_server/main/scripts/install-docker.sh | sudo bash

# Или локально
sudo bash scripts/install-docker.sh
```

Скрипт:
- ✅ Поддерживает Debian, Ubuntu, Fedora, CentOS
- ✅ Автоматически выбирает V2 или V1
- ✅ Обрабатывает зависимости и репозитории

После установки проверьте:
```bash
docker compose version     # или docker-compose version
```

---

## 🔐 Управление secrets (API ключи, пароли)

**⚠️ НИКОГДА не коммитьте .env с реальными значениями!**

Подробная документация: [docs/SECRETS.md](docs/SECRETS.md)

### Для GitHub Actions (CI/CD):

1. Перейти в **Settings → Secrets and variables → Actions**
2. Добавить каждый secret отдельно:
   ```
   TELEGRAM_TOKEN = 8127789812:AAEALs2O8hrMHGil...
   POSTGRES_PASSWORD = your_password
   TG_API_ID = 1710401
   ...
   ```

### Для локального развертывания:

```bash
# Установить переменные окружения
export TELEGRAM_TOKEN="your_token"
export POSTGRES_PASSWORD="your_password"
export TG_API_ID="1710401"
export TG_API_HASH="your_hash"
export TG_PHONE="+79991234567"

# Создать .env автоматически
bash scripts/setup-env.sh
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

# Статус сервисов (работает V1 и V2)
docker compose ps              # V2
docker-compose ps              # V1

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

**Примечание:** Команды `docker compose` (V2) и `docker-compose` (V1) полностью совместимы. Используйте то, что установлено на вашей системе.

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

## 🤖 Автоматическое развертывание через GitHub Actions

Если хотите автоматический деплой на сервер через GitHub Actions:

### 1. Добавить GitHub Secrets

```bash
# С помощью интерактивного скрипта
bash scripts/add-github-secrets.sh

# Или вручную в GitHub:
# Settings → Secrets and variables → Actions → New secret
```

### 2. Добавить SSH ключи для деплоя

```bash
# На вашем ПК (если ключа нет):
ssh-keygen -t rsa -b 4096 -f ~/.ssh/id_rsa -N ""

# Скопировать приватный ключ
cat ~/.ssh/id_rsa

# На сервере добавить публичный ключ
echo "ssh-rsa AAAA..." >> ~/.ssh/authorized_keys
```

Затем добавить в GitHub secrets:
```
SERVER_HOST = your_server.com
SERVER_USER = root
SERVER_SSH_KEY = (содержимое ~/.ssh/id_rsa)
```

### 3. При каждом push в main

```bash
git push origin main
```

GitHub автоматически:
- ✅ Получит ваш код
- ✅ Создаст .env из secrets
- ✅ Подключится к серверу по SSH
- ✅ Обновит Docker образы
- ✅ Перезапустит приложение

**Подробнее:** [docs/GITHUB-SECRETS-SETUP.md](docs/GITHUB-SECRETS-SETUP.md)

---

**Готово! 🎉** Ваш сервер должен быть запущен и работать.
