-- Организации (кабинеты). Ключ доступа хранится только хешем.
CREATE TABLE orgs (
    id          text PRIMARY KEY,
    name        text NOT NULL,
    city        text NOT NULL,
    token_hash  text NOT NULL UNIQUE,
    feed_token  text NOT NULL UNIQUE,
    created_at  timestamptz NOT NULL DEFAULT now()
);

-- Дорожные карты. org_id пуст для плана без владельца.
CREATE TABLE plans (
    id          text PRIMARY KEY,
    org_id      text REFERENCES orgs (id) ON DELETE CASCADE,
    city        text NOT NULL,
    scenario    text NOT NULL,
    title       text NOT NULL,
    start_date  date NOT NULL,
    target_date date NOT NULL,
    body        jsonb NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX plans_org_idx ON plans (org_id, created_at DESC);
CREATE INDEX plans_anonymous_idx ON plans (created_at) WHERE org_id IS NULL;

-- Штат: только код, необязательное имя и срок пропуска.
CREATE TABLE staff (
    org_id       text NOT NULL REFERENCES orgs (id) ON DELETE CASCADE,
    code         text NOT NULL,
    name         text NOT NULL DEFAULT '',
    pass_expires date NOT NULL,
    updated_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, code)
);
CREATE INDEX staff_expiry_idx ON staff (org_id, pass_expires);
