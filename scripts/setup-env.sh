#!/bin/bash

# Скрипт для создания .env файла из переменных окружения
# Использование:
#   export TELEGRAM_TOKEN="..."
#   bash scripts/setup-env.sh
#
# Или прямо:
#   TELEGRAM_TOKEN="..." bash scripts/setup-env.sh

set -e

ENV_FILE=".env"
ENV_TEMPLATE=".env.example"

# Проверить что находимся в корне проекта
if [ ! -f "$ENV_TEMPLATE" ]; then
    echo "❌ Ошибка: не найден $ENV_TEMPLATE"
    echo "   Убедитесь что находитесь в корне проекта"
    exit 1
fi

echo "🔐 Создание .env файла из переменных окружения..."
echo ""

# Создать новый .env на основе template
cp "$ENV_TEMPLATE" "$ENV_FILE.tmp"

# Функция для замены значения
set_env_var() {
    local key=$1
    local value=$2

    if [ -n "$value" ]; then
        # Экранировать специальные символы для sed
        value=$(printf '%s\n' "$value" | sed -e 's/[\/&]/\\&/g')
        sed -i "s/^${key}=.*/${key}=${value}/" "$ENV_FILE.tmp"
        echo "✅ ${key}: установлено"
    fi
}

# Применить переменные окружения
set_env_var "TELEGRAM_TOKEN" "$TELEGRAM_TOKEN"
set_env_var "NOTIFY_CHAT_ID" "$NOTIFY_CHAT_ID"
set_env_var "TG_API_ID" "$TG_API_ID"
set_env_var "TG_API_HASH" "$TG_API_HASH"
set_env_var "TG_PHONE" "$TG_PHONE"
set_env_var "TG_2FA_PASSWORD" "$TG_2FA_PASSWORD"
set_env_var "POSTGRES_PASSWORD" "$POSTGRES_PASSWORD"
set_env_var "S3_ACCESS_KEY" "$S3_ACCESS_KEY"
set_env_var "S3_SECRET_KEY" "$S3_SECRET_KEY"
set_env_var "S3_PUBLIC_URL" "$S3_PUBLIC_URL"
set_env_var "API_PORT" "$API_PORT"
set_env_var "PARSE_INTERVAL_MIN" "$PARSE_INTERVAL_MIN"
set_env_var "OPENROUTER_API_KEY" "$OPENROUTER_API_KEY"
set_env_var "OPENROUTER_MODEL" "$OPENROUTER_MODEL"
set_env_var "GEMINI_API_KEY" "$GEMINI_API_KEY"
set_env_var "GEMINI_MODEL" "$GEMINI_MODEL"
set_env_var "OBSIDIAN_VAULT_HOST_PATH" "$OBSIDIAN_VAULT_HOST_PATH"

echo ""

# Проверить что все обязательные переменные установлены
REQUIRED_VARS=("TELEGRAM_TOKEN" "POSTGRES_PASSWORD" "TG_API_ID" "TG_API_HASH" "TG_PHONE")
MISSING=()

for var in "${REQUIRED_VARS[@]}"; do
    if ! grep -q "^${var}=.*[^=]$" "$ENV_FILE.tmp"; then
        MISSING+=("$var")
    fi
done

if [ ${#MISSING[@]} -gt 0 ]; then
    echo "⚠️  Не установлены обязательные переменные:"
    printf '   - %s\n' "${MISSING[@]}"
    rm "$ENV_FILE.tmp"
    exit 1
fi

# Переместить временный файл
mv "$ENV_FILE.tmp" "$ENV_FILE"
chmod 600 "$ENV_FILE"

echo ""
echo "✅ === .env файл создан успешно ==="
echo ""
echo "📝 Содержимое (с замаскированными ключами):"
grep -v "^#" "$ENV_FILE" | grep "=" | sed 's/=.*/=***MASKED***/g'
echo ""
echo "🔒 Файл .env защищен (600 permissions)"
