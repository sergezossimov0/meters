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

Сервер слушает `127.0.0.1:8080`, миграции применяются при старте. Шлюзы запускаются профилем `gateways` после регистрации их ключей: `COMPOSE_PROFILES=gateways mise run up`.

## Локальный запуск без Docker

Нужен PostgreSQL 16. Настройки берутся из переменных окружения или флагов (`bin/server -h`), переменная окружения важнее флага:

```sh
mise run build
export DATABASE_DSN='postgres://meters@localhost:5432/meters?sslmode=disable'
export PGPASSWORD=...   # пароль отдельно от DSN
export JWT_SECRET=...   # не короче 32 байт
bin/server
```

## Структура

| Путь | Что внутри |
|---|---|
| `cmd/server`, `cmd/gateway` | точки входа: `run(ctx)`, graceful shutdown по SIGINT/SIGTERM |
| `internal/config` | флаги и переменные окружения, валидация, маскирование секретов |
| `internal/logger` | zap и middleware логирования запросов |
| `internal/httpapi` | роутер chi, `/ping`, страница `/`; `middleware` — gzip и лимит тела |
| `internal/app` | жизненный цикл HTTP-сервера, коды выхода |
| `internal/storage/postgres` | пул pgx, миграции goose |
| `migrations` | SQL-миграции, встроены в бинарник |
