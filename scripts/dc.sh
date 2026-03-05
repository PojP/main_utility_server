#!/bin/bash

# Обертка для Docker Compose V2 и V1 совместимости
# Использует docker compose (V2) если доступен, иначе docker-compose (V1)

if command -v docker &> /dev/null; then
    # Проверить есть ли docker compose V2
    if docker compose version &> /dev/null 2>&1; then
        # V2 доступен
        exec docker compose "$@"
    elif command -v docker-compose &> /dev/null; then
        # V1 доступен
        exec docker-compose "$@"
    else
        echo "❌ Ошибка: Docker Compose не установлен"
        echo ""
        echo "Установите одно из:"
        echo "  Docker Compose V2: sudo apt install docker-compose-plugin"
        echo "  Docker Compose V1: sudo apt install docker-compose"
        exit 1
    fi
else
    echo "❌ Ошибка: Docker не установлен"
    exit 1
fi
