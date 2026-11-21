# meters

Сбор показаний приборов учёта (вода, газ, электроэнергия, тепло): шлюз-симулятор → сервер → PostgreSQL. Выпускной проект курса Go.

Сервер принимает от шлюзов пакеты показаний и heartbeat, проверяет и хранит показания, находит аномалии, считает абоненту расход и начисление за месяц. Шлюз-симулятор моделирует приборы, буферизует показания на диске и отправляет их через worker pool с повторами.

## Разработка

Нужен [mise](https://mise.jdx.dev): он ставит Go и golangci-lint нужных версий.

```sh
mise install          # Go 1.26, golangci-lint v2
mise run build        # bin/server, bin/gateway
mise run check        # lint + go test -race + build
mise tasks            # все задачи
```

Интеграционные тесты поднимают PostgreSQL через testcontainers, поэтому нужен запущенный Docker:

```sh
mise run test-integration   # go test -race -tags=integration ./...
```

Если Docker работает не через `/var/run/docker.sock` (Rancher Desktop, Colima), укажите сокет в `mise.local.toml` (он не попадает в git):

```toml
[env]
DOCKER_HOST = "unix:///Users/<you>/.rd/docker.sock"
TESTCONTAINERS_DOCKER_SOCKET_OVERRIDE = "/var/run/docker.sock"
```

## Запуск в Docker

```sh
cp .env.example .env    # затем замените все change-me, например: openssl rand -hex 32
mise run up             # docker compose up -d --build: PostgreSQL + сервер
curl -i localhost:8080/ping
mise run down
```

Сервер слушает `127.0.0.1:8080`, миграции применяются при старте. Если заданы `ADMIN_LOGIN` и `ADMIN_PASSWORD`, при старте создаётся первый администратор; повторный старт его не меняет.

Проверка стенда — `mise run smoke` (`scripts/smoke.sh`):
- вход админом, регистрация абонента, `401`/`403` по ролям, добавление подтипа;
- создание шлюза, пакет показаний с подписью, повтор `batch_id`;
- `401` без ключа шлюза, `400` с неверной подписью, `403` заблокированному шлюзу.

Нужны `curl`, `jq`, `openssl` и `python3`. Каждый прогон оставляет в базе абонента `smoke-<время>`, подтип газа `smoke_<время>`, шлюз `smoke-<время>` и прибор `SMOKE-<время>` в статусе `pending`. Другой адрес сервера: `BASE_URL=http://host:port mise run smoke`.

Шлюзы запускаются профилем `gateways` после регистрации их ключей: `COMPOSE_PROFILES=gateways mise run up`.

## Локальный запуск без Docker

Нужен PostgreSQL 16. Настройки берутся из переменных окружения или флагов (`bin/server -h`), переменная окружения важнее флага:

```sh
mise run build
export DATABASE_DSN='postgres://meters@localhost:5432/meters?sslmode=disable'
export PGPASSWORD=...   # пароль отдельно от DSN
export JWT_SECRET=...   # не короче 32 байт
bin/server
```

## API

Ошибки возвращаются как `{"error": "..."}`. Защищённые методы требуют заголовок `Authorization: Bearer <token>`: без него или с плохим токеном — `401`, не та роль — `403`.

```sh
# Регистрация абонента: сразу возвращает токен; логин занят — 409
curl -s localhost:8080/api/user/register -H 'Content-Type: application/json' \
  -d '{"login": "ivan", "password": "<8–72 байта>"}'
# → {"token":"eyJ..."}

# Вход: неверная пара — 401
TOKEN=$(curl -s localhost:8080/api/user/login -H 'Content-Type: application/json' \
  -d '{"login": "ivan", "password": "<...>"}' | jq -r .token)

# Справочник типов и подтипов (любая роль)
curl -s localhost:8080/api/meter-types -H "Authorization: Bearer $TOKEN"
# → [{"code":"electricity","name":"Электроэнергия","subtypes":[{"code":"active_energy","name":"Активная энергия","unit":"kWh"}, ...]}, ...]

# Добавить тип (только admin; без type_code)
curl -s localhost:8080/api/admin/meter-types -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H 'Content-Type: application/json' -d '{"code": "steam", "name": "Пар"}'
# → 201 {"code":"steam","name":"Пар","subtypes":[]}

# Добавить подтип: с type_code, unit обязателен; код занят — 409, нет такого типа — 422
curl -s localhost:8080/api/admin/meter-types -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"type_code": "steam", "code": "process_steam", "name": "Технологический пар", "unit": "t"}'
# → 201 {"type_code":"steam","code":"process_steam","name":"Технологический пар","unit":"t"}
```

Коды типов и подтипов: 2–32 символа, первый — `a-z`, дальше `a-z`, `0-9`, `_`. Коды подтипов уникальны во всём справочнике.

### Шлюзы (админ)

```sh
# Создать шлюз: ключ показывается только в этом ответе (Cache-Control: no-store)
curl -s localhost:8080/api/admin/gateways -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H 'Content-Type: application/json' -d '{"name": "Шлюз 17"}'
# → 201 {"id":1,"name":"Шлюз 17","blocked":false,"last_heartbeat_at":null,"created_at":"...","key":"gwk_..."}

# Список шлюзов (без ключей)
curl -s localhost:8080/api/admin/gateways -H "Authorization: Bearer $ADMIN_TOKEN"

# Заблокировать / разблокировать: 200 со шлюзом, повтор — не ошибка, нет шлюза — 404
curl -s -X POST localhost:8080/api/admin/gateways/1/block -H "Authorization: Bearer $ADMIN_TOKEN"
curl -s -X POST localhost:8080/api/admin/gateways/1/unblock -H "Authorization: Bearer $ADMIN_TOKEN"
```

### API шлюзов (`/gateway/v1`)

Шлюз передаёт свой ключ в `X-Gateway-Key`. Если сервер запущен с ключом подписи (`KEY`, флаг `-k`), тело запроса подписывается: `HashSHA256: hex(HMAC-SHA256(тело, KEY))`. Подпись считается от несжатого тела. Ответ подписан тем же способом.

Порядок проверок:
1. Ключ шлюза: нет или неверный — `401`, шлюз заблокирован — `403`.
2. Подпись: нет или неверная — `400`.
3. Тело пакета.

Ответы с ошибками тоже подписаны.

```sh
# Пакет показаний; GATEWAY_KEY и KEY — в окружении
cat >batch.json <<'JSON'
{"batch_id": "gw-17-000001",
 "items": [{"device_serial": "WM-2024-0001234", "model": "WaterMeter-X1",
            "device_status": {"battery_pct": 92, "rssi": -112, "error_codes": []},
            "readings": [{"measured_at": "2026-11-21T10:00:00Z", "value": "123.456"}]}]}
JSON
SIG=$(openssl dgst -sha256 -hmac "$KEY" -r batch.json | cut -d' ' -f1)
curl -s localhost:8080/gateway/v1/readings -H 'Content-Type: application/json' \
  -H "X-Gateway-Key: $GATEWAY_KEY" -H "HashSHA256: $SIG" --data-binary @batch.json
# → {"accepted":0,"duplicates":0,"rejected":[{"device_serial":"WM-2024-0001234","index":0,"reason":"device_not_active"}]}
```

`openssl dgst -hmac` получает ключ в аргументах командной строки, и его видно в `ps`. Для примера это допустимо; `scripts/smoke.sh` считает подпись через `python3` и берёт ключ из окружения.

- **Незнакомый серийный номер** становится прибором в статусе `pending`. Его показания отклоняются с причиной `device_not_active`, пока администратор не одобрит прибор (L5).
- **Повтор `batch_id`** от того же шлюза возвращает сохранённый ответ и ничего не записывает.
- **Причины отказа** в `rejected`: `future_ts`, `too_old`, `invalid_value`, `device_not_active`, `device_blocked`, `invalid_status` (`index: -1`). `index` — позиция показания в массиве `readings` элемента.
- **`400` на весь пакет:**
  - неверный JSON или тип поля;
  - больше 5000 показаний;
  - пустой `items`;
  - неверный `batch_id`, `device_serial` или `model`.
- **Неизвестные поля** в JSON шлюза игнорируются.

## Структура

| Путь | Что внутри |
|---|---|
| `cmd/server`, `cmd/gateway` | точки входа: `run(ctx)`, graceful shutdown по SIGINT/SIGTERM |
| `internal/config` | флаги и переменные окружения, валидация, маскирование секретов |
| `internal/logger` | zap и middleware логирования запросов |
| `internal/httpapi` | роутер chi, хендлеры `/api`, `/gateway/v1`, `/ping`, страница `/`; `middleware` — gzip, лимит тела, JWT и роли, ключ шлюза, подпись `HashSHA256` |
| `internal/app` | жизненный цикл HTTP-сервера, коды выхода |
| `internal/domain` | сущности и правила: логин, пароль, коды справочника, значение показания, проверка пакета, опорное показание и `decrease`; ошибки домена |
| `internal/service` | сценарии: регистрация, вход, первый админ, справочник типов, шлюзы и их ключи, приём пакетов |
| `internal/auth` | пароли bcrypt, токены HS256 |
| `internal/sign` | HMAC-SHA256 для подписи запросов и ответов шлюзов |
| `internal/retry` | повторы при обрыве связи с БД и с сервером (паузы 1/3/5 с) |
| `internal/storage` | интерфейсы репозиториев; `memory` — для тестов, `postgres` — пул pgx, миграции goose, репозитории; `storagetest` — общие контрактные тесты |
| `scripts/smoke.sh` | проверка запущенного стенда |
| `migrations` | SQL-миграции, встроены в бинарник |
