-- Per-proxy egress location used by OpenAI accounts whose outbound timezone
-- follows their proxy. One row per proxy; location fields always belong to the
-- last successful probe of config_fingerprint (protocol/host/port/credentials
-- hash). A failed probe of the same configuration keeps the last location; a
-- probe of a changed configuration clears it.
CREATE TABLE IF NOT EXISTS proxy_exit_locations (
    proxy_id           BIGINT PRIMARY KEY REFERENCES proxies(id) ON DELETE CASCADE,
    config_fingerprint VARCHAR(64)  NOT NULL,
    exit_ip            VARCHAR(64)  NOT NULL DEFAULT '',
    country_code       VARCHAR(8)   NOT NULL DEFAULT '',
    region             VARCHAR(128) NOT NULL DEFAULT '',
    city               VARCHAR(128) NOT NULL DEFAULT '',
    timezone           VARCHAR(64)  NOT NULL DEFAULT '',
    source             VARCHAR(32)  NOT NULL DEFAULT '',
    succeeded_at       TIMESTAMPTZ,
    attempted_at       TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    next_probe_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    failure_count      INTEGER      NOT NULL DEFAULT 0,
    last_error         TEXT         NOT NULL DEFAULT '',
    updated_at         TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);
