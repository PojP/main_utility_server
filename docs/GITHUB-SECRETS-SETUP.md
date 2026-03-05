# 🔐 Настройка GitHub Secrets для CI/CD

## 📋 Список всех нужных Secrets

### 1. Перейти в Settings

GitHub → Settings → Secrets and variables → Actions

### 2. Добавить все secrets

Нажмите **New repository secret** для каждого:

#### 🤖 Telegram Bot
```
Name: TELEGRAM_TOKEN
Value: 8127789812:AAEALs2O8hrMHGil73QYMZaPIaKgkfc1MFI
```

```
Name: NOTIFY_CHAT_ID
Value: 524845066
```

#### 📱 Telegram Userbot (MTProto)
```
Name: TG_API_ID
Value: 1710401
```

```
Name: TG_API_HASH
Value: f090a51e744201a57ee4a64ae123492a
```

```
Name: TG_PHONE
Value: +79207350991
```

```
Name: TG_2FA_PASSWORD
Value: Pp545828545
```

#### 🗄️ Database
```
Name: POSTGRES_PASSWORD
Value: rssreader
```

#### 💾 MinIO (S3)
```
Name: S3_ACCESS_KEY
Value: minioadmin
```

```
Name: S3_SECRET_KEY
Value: minioadmin
```

```
Name: S3_PUBLIC_URL
Value: http://localhost:9000
```

#### 🧠 AI Services
```
Name: OPENROUTER_API_KEY
Value: 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
```

```
Name: OPENROUTER_MODEL
Value: google/gemma-3-27b-it:free
```

```
Name: GEMINI_API_KEY
Value: AIzaSyDYRXdm6kWvNKNaBeMz3LXSnvz6EOfncPU
```

```
Name: GEMINI_MODEL
Value: gemini-2.5-flash
```

#### 📝 Other
```
Name: PARSE_INTERVAL_MIN
Value: 15
```

```
Name: API_PORT
Value: 8080
```

```
Name: OBSIDIAN_VAULT_HOST_PATH
Value: /home/yartsal_fucker/sync/main_obsidin
```

---

## 🖥️ Для развертывания на сервер

Если используете workflow `server-deploy.yml`, добавьте эти secrets:

### SSH доступ к серверу

```
Name: SERVER_HOST
Value: your_server_ip_or_domain.com
```

```
Name: SERVER_USER
Value: root
```

```
Name: SERVER_SSH_KEY
Value: (содержимое вашего приватного SSH ключа)
```

```
Name: SERVER_PORT
Value: 22
(опционально, по умолчанию 22)
```

### Как получить SSH ключ для GitHub

#### На вашем локальном ПК:

```bash
# Если уже есть ключ
cat ~/.ssh/id_rsa

# Если нет, создать
ssh-keygen -t rsa -b 4096 -f ~/.ssh/id_rsa -N ""
cat ~/.ssh/id_rsa
```

#### На сервере:

```bash
# Добавить публичный ключ
echo "ssh-rsa AAAA..." >> ~/.ssh/authorized_keys
chmod 600 ~/.ssh/authorized_keys
```

---

## 🔍 Где найти значения

### TELEGRAM_TOKEN
- Напишите [@BotFather](https://t.me/botfather)
- `/newbot` → заполните название и юзернейм
- Получите токен вида `123456:ABC-DEF...`

### TG_API_ID и TG_API_HASH
- Перейдите на https://my.telegram.org
- **API development tools**
- Скопируйте App api_id и App api_hash

### TELEGRAM_PHONE
- Ваш номер телефона с кодом страны
- Формат: `+7999123456`

### POSTGRES_PASSWORD
- Выберите сильный пароль
- Только буквы, цифры, !@#$%

### S3_ACCESS_KEY / S3_SECRET_KEY
- Для MinIO используйте любые значения (они локальные)
- По умолчанию: `minioadmin` / `minioadmin`

### OPENROUTER_API_KEY
- Зарегистрируйтесь на https://openrouter.ai
- Перейдите в Keys
- Скопируйте API Key

### GEMINI_API_KEY
- Перейдите на https://aistudio.google.com/app/apikey
- Нажмите **Create API key**
- Скопируйте ключ

---

## ✅ Проверка

После добавления всех secrets:

1. Перейдите в **Actions**
2. Выберите workflow (например, **Deploy to Server**)
3. Нажмите **Run workflow**
4. Проверьте логи

**В логах не должны видны реальные значения secrets!** GitHub автоматически маскирует их.

---

## 🔄 Обновление secrets

```bash
# Если нужно изменить secret на GitHub
# Settings → Secrets and variables → Actions → нажать на secret → Update
```

Или из GitHub CLI:

```bash
# Установить GitHub CLI
brew install gh  # или apt install gh

# Залогиниться
gh auth login

# Обновить secret
gh secret set TELEGRAM_TOKEN -b "new_token_value"

# Получить список
gh secret list
```

---

## 🚨 Безопасность

- ✅ Никогда не выкладывайте реальные secrets в .env
- ✅ GitHub автоматически маскирует их в логах
- ✅ Используйте разные значения для разработки и production
- ✅ Ротируйте ключи регулярно
- ✅ Если key утек — срочно обновите через `gh secret set`

---

## 📚 Workflows использующие secrets

### `.github/workflows/server-deploy.yml`
- Деплой на физический сервер через SSH
- Использует все secrets для создания .env
- Запускается на push в main или на git tags

### Как использовать:

```bash
# После push в main
git push origin main

# Workflow автоматически:
# 1. Получит код
# 2. Подключится к серверу по SSH
# 3. Создаст .env из GitHub Secrets
# 4. Обновит Docker образы
# 5. Перезапустит контейнеры
```

---

## 🐛 Troubleshooting

### "Permission denied (publickey)"
- Проверьте что SSH ключ скопирован правильно
- Убедитесь что публичный ключ на сервере в `~/.ssh/authorized_keys`

### "secret not found"
- Проверьте название secret (case-sensitive)
- Убедитесь что secret добавлен в Settings → Secrets

### Логи не показывают ошибку
- GitHub маскирует secrets в логах для безопасности
- Подключитесь на сервер и проверьте:
  ```bash
  docker compose logs -f api
  ```

---

## 🎯 Quick Reference

```bash
# Добавить все secrets сразу (если есть gh cli)
gh secret set TELEGRAM_TOKEN -b "value"
gh secret set TG_API_ID -b "value"
gh secret set TG_API_HASH -b "value"
# ... и т.д.

# Проверить список
gh secret list

# Удалить old secret
gh secret delete OLD_SECRET_NAME
```
