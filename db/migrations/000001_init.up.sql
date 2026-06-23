CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE users (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    email text NOT NULL UNIQUE,
    password_hash text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE visitor_sessions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid REFERENCES users(id) ON DELETE SET NULL,
    anonymous_token_hash text NOT NULL UNIQUE,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    last_seen_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL
);

CREATE TABLE events (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name text NOT NULL,
    starts_at timestamptz NOT NULL,
    ends_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE ticket_types (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    event_id uuid NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    name text NOT NULL,
    total_quantity integer NOT NULL,
    sold_quantity integer NOT NULL DEFAULT 0,
    reserved_quantity integer NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    -- These CHECK constraints keep invalid stock states out of the database even if a future
    -- application change introduces a bug. The service still validates inputs, but PostgreSQL is
    -- the final authority for critical inventory invariants.
    CONSTRAINT ck_ticket_types_total_quantity_non_negative CHECK (total_quantity >= 0),
    CONSTRAINT ck_ticket_types_sold_quantity_non_negative CHECK (sold_quantity >= 0),
    CONSTRAINT ck_ticket_types_reserved_quantity_non_negative CHECK (reserved_quantity >= 0),
    CONSTRAINT ck_ticket_types_allocated_quantity_within_total
        CHECK (sold_quantity + reserved_quantity <= total_quantity)
);

CREATE INDEX ix_ticket_types_event_id ON ticket_types(event_id);

CREATE TABLE seats (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    event_id uuid NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    section text NOT NULL,
    row_name text NOT NULL,
    seat_number text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    -- A physical seat should exist once per event. Reservation concurrency is handled elsewhere,
    -- but the seat catalog itself must not contain duplicates.
    CONSTRAINT uq_seats_event_section_row_seat UNIQUE (event_id, section, row_name, seat_number)
);

CREATE INDEX ix_seats_event_id ON seats(event_id);

CREATE TABLE reservations (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    event_id uuid NOT NULL REFERENCES events(id) ON DELETE RESTRICT,
    visitor_session_id uuid NOT NULL REFERENCES visitor_sessions(id) ON DELETE RESTRICT,
    user_id uuid REFERENCES users(id) ON DELETE SET NULL,
    status text NOT NULL,
    reservation_type text NOT NULL,
    idempotency_key text NOT NULL,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    -- Idempotency is scoped to the visitor session. A retry with the same key returns the original
    -- reservation instead of creating another hold and consuming more inventory.
    CONSTRAINT ck_reservations_status
        CHECK (status IN ('reserved', 'confirmed', 'cancelled', 'expired')),
    CONSTRAINT ck_reservations_reservation_type CHECK (reservation_type IN ('quantity', 'seats')),
    CONSTRAINT uq_reservations_visitor_session_id_idempotency_key
        UNIQUE (visitor_session_id, idempotency_key)
);

CREATE INDEX ix_reservations_visitor_session_id ON reservations(visitor_session_id);
CREATE INDEX ix_reservations_user_id ON reservations(user_id);
CREATE INDEX ix_reservations_status_expires_at ON reservations(status, expires_at);

CREATE TABLE reservation_items (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    reservation_id uuid NOT NULL REFERENCES reservations(id) ON DELETE CASCADE,
    ticket_type_id uuid NOT NULL REFERENCES ticket_types(id) ON DELETE RESTRICT,
    quantity integer NOT NULL,
    status text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT ck_reservation_items_quantity_positive CHECK (quantity > 0),
    CONSTRAINT ck_reservation_items_status
        CHECK (status IN ('reserved', 'confirmed', 'cancelled', 'expired'))
);

CREATE INDEX ix_reservation_items_ticket_type_id_status
    ON reservation_items(ticket_type_id, status);

CREATE TABLE reservation_seats (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    reservation_id uuid NOT NULL REFERENCES reservations(id) ON DELETE CASCADE,
    seat_id uuid NOT NULL REFERENCES seats(id) ON DELETE RESTRICT,
    status text NOT NULL,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT ck_reservation_seats_status
        CHECK (status IN ('reserved', 'confirmed', 'cancelled', 'expired'))
);

CREATE INDEX ix_reservation_seats_seat_id_status ON reservation_seats(seat_id, status);

-- Only reserved and confirmed seat rows are active holds.
-- Cancelled and expired rows remain as history but no longer block future reservations.
CREATE UNIQUE INDEX uq_active_reservation_seat
    ON reservation_seats(seat_id)
    WHERE status IN ('reserved', 'confirmed');
