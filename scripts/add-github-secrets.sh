#!/bin/bash

# Скрипт для добавления всех secrets в GitHub репозиторий
# Требует: gh cli (GitHub Command Line Interface)
# Установка: brew install gh (macOS) или apt install gh (Linux)

set -e

echo "=== GitHub Secrets Setup ==="
echo ""

# Проверить что установлен gh cli
if ! command -v gh &> /dev/null; then
    echo "❌ Ошибка: gh CLI не установлен"
    echo ""
    echo "Установите:"
    echo "  macOS:   brew install gh"
    echo "  Ubuntu:  sudo apt install gh"
    echo "  Fedora:  sudo dnf install gh"
    echo ""
    echo "После установки: gh auth login"
    exit 1
fi

# Проверить что залогинены
if ! gh auth status &>/dev/null; then
    echo "❌ Ошибка: не залогинены в GitHub"
    echo "Запустите: gh auth login"
    exit 1
fi

echo "📝 Введите значения для secrets:"
echo ""

# Функция для безопасного ввода пароля
read_secret() {
    local prompt=$1
    local var_name=$2
    read -sp "$(echo -e '\033[33m?'$(echo -e '\033[0m') $prompt: " value
    echo ""
    eval "$var_name='$value'"
}

# Функция для обычного ввода
read_value() {
    local prompt=$1
    local var_name=$2
    read -p "? $prompt: " value
    eval "$var_name='$value'"
}

# Telegram Bot
read_value "TELEGRAM_TOKEN" TELEGRAM_TOKEN
read_value "NOTIFY_CHAT_ID (опционально, Enter пропустить)" NOTIFY_CHAT_ID

# Telegram Userbot
read_value "TG_API_ID" TG_API_ID
read_value "TG_API_HASH" TG_API_HASH
read_value "TG_PHONE (+7991234567)" TG_PHONE
read_value "TG_2FA_PASSWORD (опционально, Enter пропустить)" TG_2FA_PASSWORD

# Database
read_secret "POSTGRES_PASSWORD" POSTGRES_PASSWORD

# MinIO
read_value "S3_ACCESS_KEY (по умолчанию minioadmin)" S3_ACCESS_KEY
S3_ACCESS_KEY=${S3_ACCESS_KEY:-minioadmin}

read_value "S3_SECRET_KEY (по умолчанию minioadmin)" S3_SECRET_KEY
S3_SECRET_KEY=${S3_SECRET_KEY:-minioadmin}

read_value "S3_PUBLIC_URL (опционально)" S3_PUBLIC_URL

# AI Keys
read_value "OPENROUTER_API_KEY (опционально, Enter пропустить)" OPENROUTER_API_KEY
read_value "GEMINI_API_KEY (опционально, Enter пропустить)" GEMINI_API_KEY

# Server (optional)
read_value "SERVER_HOST (опционально для ssh деплоя)" SERVER_HOST
read_value "SERVER_USER (опционально для ssh деплоя)" SERVER_USER
read_value "SERVER_PORT (опционально, по умолчанию 22)" SERVER_PORT

echo ""
echo "🔐 Добавление secrets в GitHub..."
echo ""

# Функция для добавления secret если значение не пусто
add_secret() {
    local name=$1
    local value=$2

    if [ -n "$value" ]; then
        echo "✅ Добавляю $name..."
        gh secret set "$name" -b "$value"
    fi
}

# Добавить все secrets
add_secret "TELEGRAM_TOKEN" "$TELEGRAM_TOKEN"
add_secret "NOTIFY_CHAT_ID" "$NOTIFY_CHAT_ID"
add_secret "TG_API_ID" "$TG_API_ID"
add_secret "TG_API_HASH" "$TG_API_HASH"
add_secret "TG_PHONE" "$TG_PHONE"
add_secret "TG_2FA_PASSWORD" "$TG_2FA_PASSWORD"
add_secret "POSTGRES_PASSWORD" "$POSTGRES_PASSWORD"
add_secret "S3_ACCESS_KEY" "$S3_ACCESS_KEY"
add_secret "S3_SECRET_KEY" "$S3_SECRET_KEY"
add_secret "S3_PUBLIC_URL" "$S3_PUBLIC_URL"
add_secret "OPENROUTER_API_KEY" "$OPENROUTER_API_KEY"
add_secret "GEMINI_API_KEY" "$GEMINI_API_KEY"
add_secret "SERVER_HOST" "$SERVER_HOST"
add_secret "SERVER_USER" "$SERVER_USER"
add_secret "SERVER_PORT" "$SERVER_PORT"

# Стандартные значения
add_secret "PARSE_INTERVAL_MIN" "15"
add_secret "API_PORT" "8080"
add_secret "OPENROUTER_MODEL" "qwen/qwen-2.5-72b-instruct:free"
add_secret "GEMINI_MODEL" "gemini-2.0-flash"

echo ""
echo "✅ === Все secrets добавлены ==="
echo ""
echo "📋 Список добавленных secrets:"
gh secret list
echo ""
echo "🚀 Теперь можно запустить workflow:"
echo "   GitHub → Actions → Deploy to Server → Run workflow"
