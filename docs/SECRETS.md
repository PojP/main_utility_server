# 🔐 Управление Secrets (API ключи, пароли, токены)

## ⚠️ НИКОГДА не коммитьте .env с реальными значениями!

`.env` содержит чувствительные данные:
- Telegram токены
- API ключи (Gemini, OpenRouter)
- Пароли БД
- Учетные данные S3

## 📋 Способ 1: GitHub Secrets для CI/CD

### 1. Добавить secrets в GitHub

Перейдите в **Settings → Secrets and variables → Actions**

Добавьте каждую переменную отдельно:

```
Name: TELEGRAM_TOKEN
Value: 8127789812:AAEALs2O8hrMHGil73QYMZaPIaKgkfc1MFI

Name: OPENROUTER_API_KEY
Value: sk-or-v1-51d533...

Name: GEMINI_API_KEY
Value: AIzaSyDYRXdm6kWvNKNaBeMz3...

Name: TG_API_ID
Value: 1710401

Name: TG_API_HASH
Value: f090a51e744201a57ee4a64ae123492a

Name: POSTGRES_PASSWORD
Value: rssreader

Name: S3_ACCESS_KEY
Value: minioadmin

Name: S3_SECRET_KEY
Value: minioadmin
```

### 2. Использовать secrets в Workflow

В `.github/workflows/deploy.yml` или другом workflow:

```yaml
jobs:
  deploy:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - name: Create .env file
        env:
          TELEGRAM_TOKEN: ${{ secrets.TELEGRAM_TOKEN }}
          OPENROUTER_API_KEY: ${{ secrets.OPENROUTER_API_KEY }}
          GEMINI_API_KEY: ${{ secrets.GEMINI_API_KEY }}
          TG_API_ID: ${{ secrets.TG_API_ID }}
          TG_API_HASH: ${{ secrets.TG_API_HASH }}
          POSTGRES_PASSWORD: ${{ secrets.POSTGRES_PASSWORD }}
          S3_ACCESS_KEY: ${{ secrets.S3_ACCESS_KEY }}
          S3_SECRET_KEY: ${{ secrets.S3_SECRET_KEY }}
        run: |
          cat > .env << EOF
          TELEGRAM_TOKEN=${TELEGRAM_TOKEN}
          OPENROUTER_API_KEY=${OPENROUTER_API_KEY}
          GEMINI_API_KEY=${GEMINI_API_KEY}
          TG_API_ID=${TG_API_ID}
          TG_API_HASH=${TG_API_HASH}
          POSTGRES_PASSWORD=${POSTGRES_PASSWORD}
          S3_ACCESS_KEY=${S3_ACCESS_KEY}
          S3_SECRET_KEY=${S3_SECRET_KEY}
          EOF
```

## 🖥️ Способ 2: Deployment с секретами на сервер

### Вариант A: Копировать .env вручную (НЕБЕЗОПАСНО)

```bash
# Локально скопировать на сервер (выглядит простой, но рискованно)
scp .env user@server.com:/opt/rss-reader/
```

⚠️ **Проблема:** .env передается по SSH незащищенно

### Вариант B: Использовать SSH ключи (рекомендуется)

```bash
# На сервере создать скрипт для конфигурации
cat > /opt/rss-reader/scripts/setup-env.sh << 'EOF'
#!/bin/bash
# Запускается после clone репозитория
# Источник: переменные окружения сервера

cat > /opt/rss-reader/.env << 'ENVEOF'
TELEGRAM_TOKEN=${TELEGRAM_TOKEN}
OPENROUTER_API_KEY=${OPENROUTER_API_KEY}
GEMINI_API_KEY=${GEMINI_API_KEY}
TG_API_ID=${TG_API_ID}
TG_API_HASH=${TG_API_HASH}
POSTGRES_PASSWORD=${POSTGRES_PASSWORD}
S3_ACCESS_KEY=${S3_ACCESS_KEY}
S3_SECRET_KEY=${S3_SECRET_KEY}
ENVEOF

chmod 600 /opt/rss-reader/.env
EOF

chmod +x /opt/rss-reader/scripts/setup-env.sh
```

Потом на сервере:
```bash
# Установить переменные окружения сервера
export TELEGRAM_TOKEN="your_token"
export POSTGRES_PASSWORD="your_password"
# ... и остальные ...

# Запустить скрипт
bash /opt/rss-reader/scripts/setup-env.sh

# Проверить
cat /opt/rss-reader/.env
```

### Вариант C: Использовать 1Password/HashiCorp Vault (enterprise)

Для больших teams:

```bash
# Получить secrets из Vault
vault kv get -format=json secret/rss-reader/prod > secrets.json

# Преобразовать в .env
jq -r 'to_entries[] | "\(.key)=\(.value)"' secrets.json > .env
```

## 🔒 .gitignore (ОБЯЗАТЕЛЬНО!)

Убедитесь что `.gitignore` содержит:

```
.env
.env.local
.env.*.local
secrets/
*.key
*.pem
```

**Проверить не закомичен ли .env:**

```bash
git ls-files | grep -i env

# Если показало .env — СРОЧНО удалить:
git rm --cached .env
git commit -m "Remove .env from git history"
```

## 📝 .env.example (БЕЗ реальных значений)

Вместо реального `.env`, коммитьте `template`:

```env
# .env.example — шаблон без реальных значений

# === Bot ===
TELEGRAM_TOKEN=your_bot_token_here

# === Userbot ===
TG_API_ID=your_api_id
TG_API_HASH=your_api_hash
TG_PHONE=+79991234567

# === PostgreSQL ===
POSTGRES_PASSWORD=secure_password_here

# === AI ===
OPENROUTER_API_KEY=sk-or-v1-...
GEMINI_API_KEY=AIzaSy...

# ... и т.д.
```

**Затем копируйте и заполняйте:**
```bash
cp .env.example .env
nano .env  # Добавить реальные значения
```

## 🔐 Best Practices

### 1. Ротация ключей

Меняйте ключи регулярно:
```bash
# Новый telegram токен
/newbot в @BotFather

# Новый API ключ
На https://openrouter.ai/keys → Regenerate

# Новый пароль БД
# Необходимо также обновить все сервисы
```

### 2. Разные secrets для разных окружений

```
# Development
POSTGRES_PASSWORD_DEV=dev_password
TELEGRAM_TOKEN_DEV=dev_token

# Production
POSTGRES_PASSWORD_PROD=super_secret
TELEGRAM_TOKEN_PROD=prod_token
```

### 3. Audit логирование

```bash
# На сервере — проверить кто изменял .env
sudo ls -la /opt/rss-reader/.env
sudo stat /opt/rss-reader/.env

# Логи всех операций
sudo journalctl | grep docker
```

### 4. Удаление старых секретов

```bash
# Удалить из истории git
git filter-branch --tree-filter 'rm -f .env' HEAD

# Или использовать BFG Repo-Cleaner
bfg --delete-files .env
```

## 🚨 Если произошла утечка

**СРОЧНО:**

1. Отозвать все API ключи
   ```
   OpenRouter → Regenerate API Key
   Google AI Studio → Regenerate
   @BotFather → /revoke
   ```

2. Удалить из истории git
   ```bash
   git filter-branch -f --tree-filter 'rm -f .env' -- --all
   git push origin --force --all
   ```

3. Изменить пароли БД
   ```bash
   docker compose exec postgres psql -U rssreader -c "ALTER ROLE rssreader WITH PASSWORD 'new_password';"
   ```

4. Уведомить всех пользователей

## 📚 Ресурсы

- [GitHub Secrets docs](https://docs.github.com/en/actions/security-guides/encrypted-secrets)
- [OWASP Secrets Management](https://owasp.org/www-project-api-security/)
- [Git credential security](https://git-scm.com/book/en/v2/Git-Tools-Credential-Storage)
