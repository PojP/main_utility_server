#!/bin/bash

set -e

echo "=== RSS Reader Server Setup ==="
echo ""

# Проверить что запущено как root
if [[ $EUID -ne 0 ]]; then
   echo "⚠️  Этот скрипт требует привилегий root"
   echo "Запустите: sudo bash deploy.sh"
   exit 1
fi

# Переменные
DEPLOY_DIR="/opt/rss-reader"
GITHUB_REPO="https://github.com/PojP/main_utility_server.git"

echo "📦 Шаг 1: Установка Docker..."
if ! command -v docker &> /dev/null; then
    apt update
    apt install -y docker.io docker-compose-plugin git
    usermod -aG docker $SUDO_USER
    echo "✅ Docker установлен"
else
    echo "✅ Docker уже установлен"
fi

echo ""
echo "📂 Шаг 2: Клонирование репозитория..."
if [ ! -d "$DEPLOY_DIR" ]; then
    mkdir -p $(dirname $DEPLOY_DIR)
    git clone $GITHUB_REPO $DEPLOY_DIR
    cd $DEPLOY_DIR
    echo "✅ Репозиторий клонирован в $DEPLOY_DIR"
else
    cd $DEPLOY_DIR
    git pull origin main
    echo "✅ Репозиторий обновлен"
fi

echo ""
echo "⚙️  Шаг 3: Подготовка конфигурации..."

if [ ! -f ".env" ]; then
    cp .env.example .env
    echo ""
    echo "⚠️  Файл .env создан. Отредактируйте его:"
    echo "   nano $DEPLOY_DIR/.env"
    echo ""
    echo "   Обязательные переменные:"
    echo "   - POSTGRES_PASSWORD"
    echo "   - MINIO_ROOT_PASSWORD"
    echo "   - TELEGRAM_BOT_TOKEN"
    echo "   - TELEGRAM_API_ID"
    echo "   - TELEGRAM_API_HASH"
    echo "   - TELEGRAM_PHONE"
    echo ""
    read -p "Нажмите Enter когда конфиг заполнен..."
fi

echo ""
echo "🐳 Шаг 4: Запуск контейнеров..."
docker compose pull
docker compose up -d

# Ждем пока API не будет доступен
echo ""
echo "⏳ Ожидание запуска сервисов..."
for i in {1..30}; do
    if curl -s http://localhost:8080/api/news?limit=1 > /dev/null 2>&1; then
        echo "✅ API готов к работе"
        break
    fi
    echo "   Попытка $i/30..."
    sleep 2
done

echo ""
echo "🤖 Шаг 5: Настройка Userbot (требуется интерактивный ввод)..."
read -p "Нужно ли настроить userbot? (y/n) " -n 1 -r
echo
if [[ $REPLY =~ ^[Yy]$ ]]; then
    docker compose run -it userbot
    docker compose up userbot -d
    echo "✅ Userbot настроен"
fi

echo ""
echo "✅ === Установка завершена ==="
echo ""
echo "📊 Статус сервисов:"
docker compose ps
echo ""
echo "🌐 API доступен по адресу: http://localhost:8080"
echo ""
echo "📖 Полезные команды:"
echo "   docker compose logs -f api      - Логи API"
echo "   docker compose logs -f parser   - Логи парсера"
echo "   docker compose logs -f bot      - Логи бота"
echo "   docker compose ps               - Статус сервисов"
echo "   docker compose restart service  - Перезапустить сервис"
echo ""
echo "🔐 Для безопасности:"
echo "   1. Установите Nginx Reverse Proxy"
echo "   2. Настройте Firewall (UFW)"
echo "   3. Включите SSL сертификат"
echo "   См: docs/DEPLOYMENT.md"
echo ""
