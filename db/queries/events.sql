-- name: CreateEvent :one
INSERT INTO events (name, starts_at, ends_at)
VALUES ($1, $2, $3)
RETURNING *;

-- name: GetEventByID :one
SELECT *
FROM events
WHERE id = $1;

-- name: CreateTicketType :one
INSERT INTO ticket_types (event_id, name, total_quantity)
VALUES ($1, $2, $3)
RETURNING *;

-- name: CreateSeat :one
INSERT INTO seats (event_id, section, row_name, seat_number)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: ListTicketTypesByEvent :many
SELECT *
FROM ticket_types
WHERE event_id = $1
ORDER BY name, id;

-- name: ListTicketTypeInventoryByEvent :many
SELECT
    id,
    event_id,
    name,
    total_quantity,
    sold_quantity,
    reserved_quantity,
    total_quantity - sold_quantity - reserved_quantity AS available_quantity
FROM ticket_types
WHERE event_id = $1
ORDER BY name, id;

-- name: ListSeatsByEvent :many
SELECT *
FROM seats
WHERE event_id = $1
ORDER BY section, row_name, seat_number, id;

-- name: ListSeatInventoryByEvent :many
SELECT
    s.id,
    s.event_id,
    s.section,
    s.row_name,
    s.seat_number,
    CASE
        WHEN rs.status = 'confirmed' THEN 'confirmed'
        WHEN rs.status = 'reserved' THEN 'reserved'
        ELSE 'available'
    END AS status
FROM seats AS s
LEFT JOIN reservation_seats AS rs
    ON rs.seat_id = s.id
    AND rs.status IN ('reserved', 'confirmed')
WHERE s.event_id = $1
ORDER BY s.section, s.row_name, s.seat_number, s.id;

