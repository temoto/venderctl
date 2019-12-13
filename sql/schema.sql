CREATE EXTENSION IF NOT EXISTS hstore;

CREATE TABLE ingest (
    received timestamp WITH time zone NOT NULL DEFAULT CURRENT_TIMESTAMP,
    vmid int4 NOT NULL,
    done boolean NOT NULL DEFAULT FALSE,
    raw bytea NOT NULL
);

CREATE INDEX ON ingest (received)
WHERE (NOT done);

-- Current machine state, update-heavy; arguably bad choice for regular SQL table
CREATE TABLE state (
    vmid int4 NOT NULL,
    state int4 NOT NULL,
    received timestamp WITH time zone NOT NULL
);

CREATE UNIQUE INDEX ON state (vmid) INCLUDE (state, received) WITH (fillfactor = 10);

CREATE TABLE inventory (
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

CREATE UNIQUE INDEX ON inventory (vmid) WITH (fillfactor = 10)
WHERE
    at_service;

CREATE UNIQUE INDEX ON inventory (vmid) WITH (fillfactor = 10)
WHERE
    NOT at_service;

-- Recent reported errors
CREATE TABLE error (
    vmid int4 NOT NULL,
    vmtime timestamp WITH time zone NOT NULL,
    received timestamp WITH time zone NOT NULL,
    app_version text NULL,
    code int4 NULL,
    message text NOT NULL,
    count int4 NULL
);

CREATE INDEX ON error (vmid, vmtime DESC) INCLUDE (code);

CREATE TABLE trans (
    vmid int4 NOT NULL,
    vmtime timestamp WITH time zone NOT NULL,
    received timestamp WITH time zone NOT NULL,
    menu_code text NOT NULL,
    options int[],
    price int4 NOT NULL,
    method int NOT NULL
);

CREATE INDEX ON trans (vmtime);

CREATE UNIQUE INDEX ON trans (vmid, vmtime);

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

