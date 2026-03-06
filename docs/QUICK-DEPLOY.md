# 🚀 Быстрый локальный деплой (без ожидания)

## Как скачать контейнеры с .env из GitHub

### Шаг 1: Запустить workflow для подготовки .env

1. Перейдите на **GitHub → Actions**
2. Выберите **Prepare .env Artifact**
3. Нажмите **Run workflow**
4. Выберите **main** ветку
5. Нажмите **Run workflow**

### Шаг 2: Подождите 1-2 минуты

Workflow создаст `.env` файл из ваших GitHub Secrets.

### Шаг 3: Скачайте .env

После завершения workflow:
1. Откройте **Prepare .env Artifact** (последний запуск)
2. Найдите раздел **Artifacts**
3. Скачайте **env-file**
4. Распакуйте, внутри будет файл `.env`

### Шаг 4: Переместите .env на сервер

```bash
# На вашем ПК
scp ~/Downloads/env-file/...env user@server.com:/opt/main_utility_server/.env
```

Или скопируйте содержимое вручную:
```bash
# На сервере
nano /opt/main_utility_server/.env
# Вставьте содержимое скачанного файла
```

### Шаг 5: Запустите контейнеры

```bash
cd /opt/main_utility_server

# Обновить образы (скачать последние)
docker compose -f docker-compose.yml -f docker-compose.prod.yml pull

# Запустить
docker compose -f docker-compose.yml -f docker-compose.prod.yml up -d

# Проверить статус
docker compose ps
```

---

## 📊 Альтернатива: Скачать образы на локальный ПК

Если хотите протестировать локально перед сервером:

### 1️⃣ Скачать образы из GHCR

```bash
# Залогиниться в GitHub Container Registry
echo $GITHUB_TOKEN | docker login ghcr.io -u $GITHUB_USERNAME --password-stdin

# Скачать образы
docker pull ghcr.io/pojp/rss-reader:main-bot
docker pull ghcr.io/pojp/rss-reader:main-parser
docker pull ghcr.io/pojp/rss-reader:main-api
docker pull ghcr.io/pojp/rss-reader:main-userbot
```

### 2️⃣ Скачать .env артефакт

(см. выше)

### 3️⃣ Запустить локально

```bash
# Поместить .env в текущую папку
docker compose -f docker-compose.yml -f docker-compose.prod.yml up -d

# Проверить
curl http://localhost:8080/api/news?limit=1
```

---

## 🔄 Полностью автоматический деплой

Если хотите полностью автоматический деплой (без ручного скачивания):

1. **Установить SSH ключи в GitHub Secrets** (см. `docs/GITHUB-SECRETS-SETUP.md`)
2. **Просто делать push:**
   ```bash
   git push origin main
   ```
3. **GitHub Actions автоматически:**
   - Собьет образы
   - Создаст .env
   - Деплойнет на сервер

---

## 🚨 Проблемы и решения

### "не могу скачать .env артефакт"
- Убедитесь что GitHub Secrets установлены (Settings → Secrets)
- Проверьте что workflow завершился успешно (Actions tab)

### ".env содержит пустые значения"
- Добавьте все необходимые Secrets в GitHub
- Перезапустите workflow

### "Permission denied при scp"
- Проверьте что можете подключиться по SSH:
  ```bash
  ssh user@server.com "ls -la"
  ```

---

## 💡 Совет

Используйте этот workflow когда:
- ✅ Хотите протестировать на своем сервере
- ✅ Не хотите ждать автоматического деплоя
- ✅ Нужно ручное управление обновлениями

Используйте полный автоматический деплой когда:
- ✅ SSH ключи уже настроены
- ✅ Хотите CI/CD без ручного участия
- ✅ Каждый push = автоматический деплой
