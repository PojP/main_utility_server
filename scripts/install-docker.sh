#!/bin/bash

# Скрипт установки Docker и Docker Compose на разные ОС
# Поддерживает: Ubuntu, Debian, Fedora, CentOS

set -e

if [[ $EUID -ne 0 ]]; then
   echo "⚠️  Этот скрипт требует привилегий root (sudo)"
   exit 1
fi

echo "=== Установка Docker и Docker Compose ==="
echo ""

# Определить ОС
if [ -f /etc/os-release ]; then
    . /etc/os-release
    OS=$NAME
    VER=$VERSION_ID
else
    echo "❌ Не удалось определить ОС"
    exit 1
fi

echo "📋 Обнаружена ОС: $OS $VER"
echo ""

# Ubuntu/Debian
if [[ "$OS" == *"Ubuntu"* ]] || [[ "$OS" == *"Debian"* ]]; then
    echo "🔧 Установка для Debian/Ubuntu..."

    apt update
    apt install -y ca-certificates curl gnupg lsb-release git

    # Добавить Docker репозиторий
    mkdir -p /etc/apt/keyrings
    curl -fsSL https://download.docker.com/linux/debian/gpg | gpg --dearmor -o /etc/apt/keyrings/docker.gpg

    DISTRO=$(lsb_release -cs)
    echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/debian $DISTRO stable" | tee /etc/apt/sources.list.d/docker.list > /dev/null

    apt update
    apt install -y docker-ce docker-ce-cli containerd.io

    # Установить Compose V2
    if apt install -y docker-compose-plugin 2>/dev/null; then
        echo "✅ Установлен Docker Compose V2 (docker compose)"
    else
        echo "⚠️  Docker Compose V2 недоступен, устанавливаю V1..."
        apt install -y docker-compose
        echo "✅ Установлен Docker Compose V1 (docker-compose)"
    fi

# Fedora/RHEL/CentOS
elif [[ "$OS" == *"Fedora"* ]] || [[ "$OS" == *"Red Hat"* ]] || [[ "$OS" == *"CentOS"* ]]; then
    echo "🔧 Установка для Fedora/RHEL/CentOS..."

    yum install -y dnf-plugins-core
    dnf config-manager --add-repo https://download.docker.com/linux/fedora/docker-ce.repo

    dnf install -y docker-ce docker-ce-cli containerd.io

    # Установить Compose V2
    if dnf install -y docker-compose-plugin 2>/dev/null; then
        echo "✅ Установлен Docker Compose V2 (docker compose)"
    else
        echo "⚠️  Docker Compose V2 недоступен, устанавливаю V1..."
        dnf install -y docker-compose
        echo "✅ Установлен Docker Compose V1 (docker-compose)"
    fi

else
    echo "❌ Поддерживаемые ОС: Ubuntu, Debian, Fedora, RHEL, CentOS"
    echo "   Обнаружена: $OS"
    exit 1
fi

echo ""
echo "🚀 Запуск Docker демона..."
systemctl start docker
systemctl enable docker

echo ""
echo "📝 Добавление пользователя в группу docker..."
if [ -n "$SUDO_USER" ]; then
    usermod -aG docker $SUDO_USER
    echo "✅ Пользователь $SUDO_USER добавлен в группу docker"
    echo "   Вам нужно перелогиниться: exec su - $SUDO_USER"
else
    echo "⚠️  SUDO_USER не определен. Добавьте вручную: usermod -aG docker <username>"
fi

echo ""
echo "✅ === Установка завершена ==="
echo ""

# Проверить версии
echo "📦 Установленные версии:"
docker --version

if docker compose version &>/dev/null 2>&1; then
    docker compose version
elif command -v docker-compose &>/dev/null; then
    docker-compose version
fi

echo ""
echo "🎉 Docker готов к использованию!"
echo ""
echo "Следующие шаги:"
echo "  1. Перелогиниться (если был добавлен в группу docker): exec su - \$USER"
echo "  2. Запустить скрипт развертывания: bash scripts/deploy.sh"
