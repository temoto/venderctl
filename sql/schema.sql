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

-- Recent reported errors
CREATE TABLE error (
    vmid int4 NOT NULL,
    vmtime timestamp WITH time zone NOT NULL,
    received timestamp WITH time zone NOT NULL,
    code int4 NULL,
    message text NOT NULL,
    count int4 NULL
);

CREATE INDEX ON error (vmid, vmtime DESC) INCLUDE (code);

CREATE TABLE trans (
    vmid int4 NOT NULL,
    vmtime timestamp WITH time zone NOT NULL,
    received timestamp WITH time zone NOT NULL,
    menu_code int4 NOT NULL,
    options int[],
    price int4 NOT NULL,
    method int NOT NULL
);

CREATE INDEX ON trans (vmtime);

CREATE UNIQUE INDEX ON trans (vmid, vmtime);

