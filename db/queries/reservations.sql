-- name: CreateReservation :one
INSERT INTO reservations (
    event_id,
    visitor_session_id,
    user_id,
    status,
    reservation_type,
    idempotency_key,
    expires_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: GetReservationByID :one
SELECT *
FROM reservations
WHERE id = $1;

-- name: LockReservationForUpdate :one
-- Lifecycle transitions lock the reservation header first. This single row serializes confirm,
-- cancel and expiration so stock counters cannot be moved twice for the same reservation.
SELECT *
FROM reservations
WHERE id = $1
FOR UPDATE;

-- name: GetReservationByIdempotencyKey :one
SELECT *
FROM reservations
WHERE visitor_session_id = $1
  AND idempotency_key = $2;

-- name: GetQuantityReservationByIdempotencyKey :one
SELECT
    r.id AS reservation_id,
    r.status,
    r.reservation_type,
    r.expires_at,
    ri.ticket_type_id,
    ri.quantity
FROM reservations AS r
JOIN reservation_items AS ri
    ON ri.reservation_id = r.id
WHERE r.visitor_session_id = $1
  AND r.idempotency_key = $2
  AND r.reservation_type = 'quantity'
ORDER BY ri.id
LIMIT 1;

-- name: GetSeatReservationByIdempotencyKey :many
SELECT
    r.id AS reservation_id,
    r.status,
    r.reservation_type,
    r.expires_at,
    rs.seat_id
FROM reservations AS r
JOIN reservation_seats AS rs
    ON rs.reservation_id = r.id
WHERE r.visitor_session_id = $1
  AND r.idempotency_key = $2
  AND r.reservation_type = 'seats'
ORDER BY rs.seat_id;

-- name: ReserveTicketQuantity :one
-- This UPDATE is intentionally atomic: PostgreSQL checks available stock and increments
-- reserved_quantity in the same statement, so concurrent requests cannot reserve the same unit.
UPDATE ticket_types
SET reserved_quantity = reserved_quantity + $3
WHERE id = $1
  AND event_id = $2
  AND total_quantity - sold_quantity - reserved_quantity >= $3
RETURNING id, total_quantity, sold_quantity, reserved_quantity;

-- name: LockEventSeats :many
-- Seat locks are acquired in a stable order. If two requests ask for [A, B] and [B, A], both
-- transactions lock rows by id, reducing deadlock risk.
SELECT id
FROM seats
WHERE event_id = $1
  AND id = ANY($2::uuid[])
ORDER BY id
FOR UPDATE;

-- name: ListExpiredActiveReservationIDsForSeats :many
SELECT DISTINCT reservation_id
FROM reservation_seats
WHERE seat_id = ANY($1::uuid[])
  AND status = 'reserved'
  AND expires_at <= $2
ORDER BY reservation_id;

-- name: LockReservationsByID :many
SELECT id
FROM reservations
WHERE id = ANY($1::uuid[])
ORDER BY id
FOR UPDATE;

-- name: ListExpiredReservationsForUpdate :many
-- SKIP LOCKED lets multiple expiration workers share the queue. Rows already locked by one
-- worker are skipped by the others instead of being processed twice.
SELECT *
FROM reservations
WHERE status = 'reserved'
  AND expires_at < $1
ORDER BY expires_at, id
FOR UPDATE SKIP LOCKED
LIMIT $2;

-- name: UpdateReservationStatus :one
UPDATE reservations
SET status = $2,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: UpdateReservationItemsStatus :exec
UPDATE reservation_items
SET status = $2,
    updated_at = now()
WHERE reservation_id = $1
  AND status = $3;

-- name: UpdateReservationSeatsStatus :exec
UPDATE reservation_seats
SET status = $2,
    updated_at = now()
WHERE reservation_id = $1
  AND status = $3;

-- name: ReleaseTicketQuantity :one
UPDATE ticket_types
SET reserved_quantity = reserved_quantity - $2,
    updated_at = now()
WHERE id = $1
  AND reserved_quantity >= $2
RETURNING id, total_quantity, sold_quantity, reserved_quantity;

-- name: ConfirmTicketQuantity :one
UPDATE ticket_types
SET reserved_quantity = reserved_quantity - $2,
    sold_quantity = sold_quantity + $2,
    updated_at = now()
WHERE id = $1
  AND reserved_quantity >= $2
RETURNING id, total_quantity, sold_quantity, reserved_quantity;

-- name: ExpireReservationSeatsByReservationIDs :exec
UPDATE reservation_seats
SET status = 'expired',
    updated_at = now()
WHERE reservation_id = ANY($1::uuid[])
  AND status = 'reserved';

-- name: ExpireReservationsByIDs :exec
UPDATE reservations
SET status = 'expired',
    updated_at = now()
WHERE id = ANY($1::uuid[])
  AND status = 'reserved';

-- name: CreateReservationItem :one
INSERT INTO reservation_items (
    reservation_id,
    ticket_type_id,
    quantity,
    status
)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: CreateReservationSeat :one
INSERT INTO reservation_seats (
    reservation_id,
    seat_id,
    status,
    expires_at
)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: ListReservationItems :many
SELECT *
FROM reservation_items
WHERE reservation_id = $1
ORDER BY ticket_type_id, id;

-- name: ListReservationSeats :many
SELECT *
FROM reservation_seats
WHERE reservation_id = $1
ORDER BY seat_id, id;
