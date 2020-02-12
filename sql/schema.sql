CREATE EXTENSION IF NOT EXISTS hstore;

CREATE TABLE IF NOT EXISTS catalog (
    vmid int4 NOT NULL,
    code text NOT NULL,
    name text NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_catalog_vmid_code_name ON catalog (vmid, code) INCLUDE (name);

CREATE TABLE IF NOT EXISTS ingest (
    received timestamp WITH time zone NOT NULL DEFAULT CURRENT_TIMESTAMP,
    vmid int4 NOT NULL,
    done boolean NOT NULL DEFAULT FALSE,
    raw bytea NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_ingest_received ON ingest (received)
WHERE (NOT done);

-- Current machine state, update-heavy; arguably bad choice for regular SQL table
CREATE TABLE IF NOT EXISTS state (
    vmid int4 NOT NULL,
    state int4 NOT NULL,
    received timestamp WITH time zone NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_state_vmid_state_received ON state (vmid) INCLUDE (state, received) WITH (fillfactor = 10);

CREATE TABLE IF NOT EXISTS inventory (
    vmid int4 NOT NULL,
    at_service bool NOT NULL,
    vmtime timestamp WITH time zone NOT NULL,
    received timestamp WITH time zone NOT NULL,
    inventory hstore,
    cashbox_bill hstore,
    cashbox_coin hstore,
    change_bill hstore,
    change_coin hstore
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_inventory_vmid_service ON inventory (vmid) WITH (fillfactor = 10)
WHERE
    at_service;

CREATE UNIQUE INDEX IF NOT EXISTS idx_inventory_vmid_not_service ON inventory (vmid) WITH (fillfactor = 10)
WHERE
    NOT at_service;

-- Recent reported errors
CREATE TABLE IF NOT EXISTS error (
    vmid int4 NOT NULL,
    vmtime timestamp WITH time zone NOT NULL,
    received timestamp WITH time zone NOT NULL,
    app_version text NULL,
    code int4 NULL,
    message text NOT NULL,
    count int4 NULL
);

CREATE INDEX IF NOT EXISTS idx_error_vmid_vmtime_code ON error (vmid, vmtime DESC) INCLUDE (code);

CREATE TYPE tax_job_state AS enum (
    'sched',
    'busy',
    'final',
    'help'
);

CREATE TABLE IF NOT EXISTS tax_job (
    id bigserial PRIMARY KEY,
    state tax_job_state NOT NULL,
    created timestamp WITH time zone NOT NULL,
    modified timestamp WITH time zone NOT NULL,
    scheduled timestamp WITH time zone NULL,
    worker text,
    processor text,
    ext_id text,
    ops jsonb,
    data jsonb,
    gross int4 NULL,
    notes text[],
    CHECK (NOT (state = 'sched' AND scheduled IS NULL)),
    CHECK (NOT (state = 'busy' AND worker IS NULL))
);

CREATE INDEX IF NOT EXISTS idx_tax_job_sched ON tax_job (scheduled, modified)
WHERE
    state = 'sched';

CREATE INDEX IF NOT EXISTS idx_tax_job_help ON tax_job (modified)
WHERE
    state = 'help';

CREATE TABLE IF NOT EXISTS trans (
    vmid int4 NOT NULL,
    vmtime timestamp WITH time zone NOT NULL,
    received timestamp WITH time zone NOT NULL,
    menu_code text NOT NULL,
    options int[],
    price int4 NOT NULL,
    method int NOT NULL,
    tax_job_id bigint NULL REFERENCES tax_job (id) ON DELETE SET NULL ON UPDATE RESTRICT
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_trans_vmid_vmtime ON trans (vmid, vmtime);

CREATE INDEX IF NOT EXISTS idx_trans_vmtime ON trans (vmtime);

CREATE OR REPLACE FUNCTION state_update (arg_vmid int4, arg_state int4)
    RETURNS int4
    AS $$
DECLARE
    old_state int4 = NULL;
BEGIN
    SELECT
        state INTO old_state
    FROM
        state
    WHERE
        vmid = arg_vmid
    LIMIT 1
    FOR UPDATE;
    INSERT INTO state (vmid, state, received)
        VALUES (arg_vmid, arg_state, CURRENT_TIMESTAMP)
    ON CONFLICT (vmid)
        DO UPDATE SET
            state = excluded.state, received = excluded.received;
    RETURN old_state;
END;
$$
LANGUAGE plpgsql;

CREATE OR REPLACE VIEW tax_job_help AS
SELECT
    *
FROM
    tax_job
WHERE
    state = 'help'
ORDER BY
    modified;

CREATE OR REPLACE FUNCTION tax_job_take (arg_worker text)
    RETURNS SETOF tax_job
    AS $$
    UPDATE
        tax_job
    SET
        state = 'busy',
        worker = arg_worker
    WHERE
        state = 'sched'
        AND scheduled <= CURRENT_TIMESTAMP
        AND id = (
            SELECT
                id
            FROM
                tax_job
            WHERE
                state = 'sched'
                AND scheduled <= CURRENT_TIMESTAMP
            ORDER BY
                scheduled,
                modified
            LIMIT 1
            FOR UPDATE
                SKIP LOCKED)
    RETURNING
        *;

$$
LANGUAGE sql;

CREATE OR REPLACE FUNCTION tax_job_trans (t trans)
    RETURNS tax_job
    AS $$
    # print_strict_params ON
DECLARE
    tjd jsonb;
    ops jsonb;
    tj tax_job;
    name text;
BEGIN
    -- lock trans row
    PERFORM
        1
    FROM
        trans
    WHERE (vmid, vmtime) = (t.vmid,
        t.vmtime)
LIMIT 1
FOR UPDATE;
    -- if trans already has tax_job assigned, just return it
    IF t.tax_job_id IS NOT NULL THEN
        SELECT
            * INTO STRICT tj
        FROM
            tax_job
        WHERE
            id = t.tax_job_id;
        RETURN tj;
    END IF;
    -- op code to human friendly name via catalog
    SELECT
        catalog.name INTO name
    FROM
        catalog
    WHERE (vmid, code) = (t.vmid,
        t.menu_code);
    IF NOT found THEN
        name := '#' || t.menu_code;
    END IF;
    ops := jsonb_build_array (jsonb_build_object('vmid', t.vmid, 'time', t.vmtime, 'name', name, 'code', t.menu_code, 'amount', 1, 'price', t.price, 'method', t.method));
    INSERT INTO tax_job (state, created, modified, scheduled, processor, ops, gross)
        VALUES ('sched', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, 'ru2019', ops, t.price)
    RETURNING
        * INTO STRICT tj;
    UPDATE
        trans
    SET
        tax_job_id = tj.id
    WHERE (vmid, vmtime) = (t.vmid,
        t.vmtime);
    RETURN tj;
END;
$$
LANGUAGE plpgsql;

CREATE OR REPLACE FUNCTION tax_job_maint_after ()
    RETURNS TRIGGER
    AS $$
BEGIN
    CASE new.state
    WHEN 'final' THEN
        NOTIFY tax_job_final;
    WHEN 'help' THEN
        NOTIFY tax_job_help;
    WHEN 'sched' THEN
        NOTIFY tax_job_sched;
    ELSE
        NULL;
    END CASE;
    RETURN NEW;
END;
$$
LANGUAGE plpgsql;

CREATE TRIGGER tax_job_maint_after
    AFTER INSERT OR UPDATE ON tax_job
    FOR EACH ROW
    EXECUTE PROCEDURE tax_job_maint_after ();

CREATE OR REPLACE FUNCTION tax_job_maint_before ()
    RETURNS TRIGGER
    AS $$
BEGIN
    IF new.state = 'final' THEN
        new.scheduled = NULL;
    END IF;
    RETURN NEW;
END;
$$
LANGUAGE plpgsql;

CREATE TRIGGER tax_job_maint_before
    BEFORE INSERT OR UPDATE ON tax_job
    FOR EACH ROW
    EXECUTE PROCEDURE tax_job_maint_before ();

CREATE OR REPLACE FUNCTION tax_job_modified ()
    RETURNS TRIGGER
    AS $$
BEGIN
    new.modified := CURRENT_TIMESTAMP;
    RETURN NEW;
END;
$$
LANGUAGE plpgsql;

CREATE TRIGGER tax_job_modified
    BEFORE UPDATE ON tax_job
    FOR EACH ROW
    WHEN ((new.ext_id IS DISTINCT FROM old.ext_id) OR (new.data IS DISTINCT FROM old.data) OR (new.notes IS DISTINCT FROM old.notes))
    EXECUTE PROCEDURE tax_job_modified ();

CREATE OR REPLACE FUNCTION trans_tax_trigger ()
    RETURNS TRIGGER
    AS $$
BEGIN
    PERFORM
        tax_job_trans (new);
    RETURN new;
END;
$$
LANGUAGE plpgsql;

CREATE TRIGGER trans_tax
    AFTER INSERT ON trans
    FOR EACH ROW
    EXECUTE PROCEDURE trans_tax_trigger ();

CREATE OR REPLACE FUNCTION vmstate (s int4)
    RETURNS text
    AS $$
    -- TODO generate from tele.proto
    -- Invalid = 0;
    -- Boot = 1;
    -- Nominal = 2;
    -- Disconnected = 3;
    -- Problem = 4;
    -- Service = 5;
    -- Lock = 6;
    SELECT
        CASE WHEN s = 0 THEN
            'Invalid'
        WHEN s = 1 THEN
            'Boot'
        WHEN s = 2 THEN
            'Nominal'
        WHEN s = 3 THEN
            'Disconnected'
        WHEN s = 4 THEN
            'Problem'
        WHEN s = 5 THEN
            'Service'
        WHEN s = 6 THEN
            'Lock'
        ELSE
            'unknown:' || s
        END
$$
LANGUAGE sql
IMMUTABLE
    RETURNS NULL ON NULL INPUT;

