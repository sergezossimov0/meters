-- Initial schema of meters-lite (spec п. 9).

-- +goose Up
CREATE TABLE users (
    id            bigserial PRIMARY KEY,
    login         text NOT NULL UNIQUE,
    password_hash text NOT NULL,                     -- bcrypt
    role          text NOT NULL CHECK (role IN ('admin', 'customer')),
    created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE meter_types (                          -- water, gas, electricity, heat
    id    smallserial PRIMARY KEY,
    code  text NOT NULL UNIQUE,
    name  text NOT NULL
);

CREATE TABLE meter_subtypes (                       -- hot water, natural gas, ...
    id       smallserial PRIMARY KEY,
    type_id  smallint NOT NULL REFERENCES meter_types(id),
    code     text NOT NULL UNIQUE,
    name     text NOT NULL,
    unit     text NOT NULL                          -- m3, kWh, Gcal
);

CREATE TABLE gateways (
    id                 bigserial PRIMARY KEY,
    name               text NOT NULL,
    key_hash           bytea NOT NULL UNIQUE,       -- sha256 of the API key
    blocked            boolean NOT NULL DEFAULT false,
    last_heartbeat_at  timestamptz,
    created_at         timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE gateway_status (                       -- last accepted Heartbeat snapshot of a gateway
    gateway_id          bigint PRIMARY KEY REFERENCES gateways(id),
    boot_id             uuid NOT NULL,
    seq                 bigint NOT NULL CHECK (seq > 0),
    sent_at             timestamptz NOT NULL,
    report_interval_s   integer NOT NULL CHECK (report_interval_s BETWEEN 1 AND 3600),
    uptime_s            bigint NOT NULL CHECK (uptime_s >= 0),
    buffered_readings   bigint NOT NULL CHECK (buffered_readings >= 0),
    devices_heard       integer NOT NULL CHECK (devices_heard >= 0),
    readings_collected  bigint NOT NULL CHECK (readings_collected >= 0),  -- counter: total since boot_id
    batches_sent        bigint NOT NULL CHECK (batches_sent >= 0),
    send_errors         bigint NOT NULL CHECK (send_errors >= 0),
    send_retries        bigint NOT NULL CHECK (send_retries >= 0),
    mem_total_bytes     bigint NOT NULL CHECK (mem_total_bytes >= 0),
    mem_free_bytes      bigint NOT NULL CHECK (mem_free_bytes BETWEEN 0 AND mem_total_bytes),
    cpu_pct             double precision[] NOT NULL CHECK (cardinality(cpu_pct) BETWEEN 1 AND 256),
    disk_free_bytes     bigint NOT NULL CHECK (disk_free_bytes >= 0),
    heap_alloc_bytes    bigint NOT NULL CHECK (heap_alloc_bytes >= 0),
    goroutines          integer NOT NULL CHECK (goroutines >= 0),
    received_at         timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE devices (
    id             bigserial PRIMARY KEY,
    serial         text NOT NULL UNIQUE,
    model          text NOT NULL,
    status         text NOT NULL CHECK (status IN ('pending', 'active', 'blocked', 'rejected')),
    subtype_id     smallint REFERENCES meter_subtypes(id),   -- set on approval
    owner_id       bigint REFERENCES users(id),
    address        text,
    initial_value  numeric(15,3),
    gateway_id     bigint NOT NULL REFERENCES gateways(id),  -- gateway the device first appeared through
    created_at     timestamptz NOT NULL DEFAULT now(),
    activated_at   timestamptz,
    CHECK (status <> 'active' OR (subtype_id IS NOT NULL AND owner_id IS NOT NULL AND initial_value IS NOT NULL))
);
CREATE INDEX devices_owner_idx ON devices (owner_id);           -- customer: "my devices"
CREATE INDEX devices_status_idx ON devices (status);            -- admin: ?status=pending, worker: active

CREATE TABLE readings (
    device_id    bigint NOT NULL REFERENCES devices(id),
    measured_at  timestamptz NOT NULL,
    value        numeric(15,3) NOT NULL,
    is_decrease  boolean NOT NULL DEFAULT false,
    gateway_id   bigint NOT NULL REFERENCES gateways(id),
    received_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (device_id, measured_at)
);
CREATE INDEX readings_received_at_idx ON readings (received_at);   -- "readings in the last hour" on the status page

CREATE TABLE device_status (                        -- last technical state of a device
    device_id    bigint PRIMARY KEY REFERENCES devices(id),
    battery_pct  smallint CHECK (battery_pct BETWEEN 0 AND 100),
    rssi         smallint CHECK (rssi BETWEEN -150 AND 0),
    error_codes  text[] NOT NULL DEFAULT '{}',
    gateway_id   bigint NOT NULL REFERENCES gateways(id),    -- gateway it came through
    reported_at  timestamptz NOT NULL,                       -- moment the state refers to
    updated_at   timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE gateway_batches (                      -- batch idempotency
    gateway_id  bigint NOT NULL REFERENCES gateways(id),
    batch_id    text NOT NULL,
    result      jsonb NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (gateway_id, batch_id)
);
CREATE INDEX gateway_batches_created_at_idx ON gateway_batches (created_at);  -- retention cleanup

CREATE TABLE anomalies (
    id           bigserial PRIMARY KEY,
    device_id    bigint NOT NULL REFERENCES devices(id),
    code         text NOT NULL CHECK (code IN ('no_data', 'decrease', 'low_battery', 'device_error')),
    status       text NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'resolved')),
    started_at   timestamptz NOT NULL,
    resolved_at  timestamptz,
    details      jsonb NOT NULL DEFAULT '{}',
    CHECK ((status = 'resolved') = (resolved_at IS NOT NULL))
);
CREATE UNIQUE INDEX anomalies_open_uq ON anomalies (device_id, code) WHERE status = 'open';
CREATE INDEX anomalies_device_idx ON anomalies (device_id, started_at);  -- needs_review for a month

CREATE TABLE tariffs (
    id          bigserial PRIMARY KEY,
    subtype_id  smallint NOT NULL REFERENCES meter_subtypes(id),
    price       numeric(14,4) NOT NULL CHECK (price >= 0),
    valid_from  date NOT NULL,
    UNIQUE (subtype_id, valid_from)
);

-- +goose Down
DROP TABLE tariffs;
DROP TABLE anomalies;
DROP TABLE gateway_batches;
DROP TABLE device_status;
DROP TABLE readings;
DROP TABLE devices;
DROP TABLE gateway_status;
DROP TABLE gateways;
DROP TABLE meter_subtypes;
DROP TABLE meter_types;
DROP TABLE users;
