-- name: ListStressEventIDs :many
SELECT id
FROM events
WHERE name LIKE $1
ORDER BY id;

-- name: DeleteStressReservationSeats :exec
DELETE FROM reservation_seats
WHERE reservation_id IN (
    SELECT id
    FROM reservations
    WHERE event_id = ANY($1::uuid[])
);

-- name: DeleteStressReservationItems :exec
DELETE FROM reservation_items
WHERE reservation_id IN (
    SELECT id
    FROM reservations
    WHERE event_id = ANY($1::uuid[])
);

-- name: DeleteStressReservations :exec
DELETE FROM reservations
WHERE event_id = ANY($1::uuid[]);

-- name: DeleteStressEvents :execrows
DELETE FROM events
WHERE id = ANY($1::uuid[]);

-- name: ListNegativeTicketQuantityViolations :many
SELECT
    id,
    total_quantity,
    sold_quantity,
    reserved_quantity
FROM ticket_types
WHERE total_quantity < 0
   OR sold_quantity < 0
   OR reserved_quantity < 0
ORDER BY id;

-- name: ListOversoldTicketQuantityViolations :many
SELECT
    id,
    total_quantity,
    sold_quantity,
    reserved_quantity
FROM ticket_types
WHERE sold_quantity + reserved_quantity > total_quantity
ORDER BY id;

-- name: ListDuplicateActiveSeatViolations :many
SELECT
    seat_id,
    COUNT(*)::bigint AS active_count
FROM reservation_seats
WHERE status IN ('reserved', 'confirmed')
GROUP BY seat_id
HAVING COUNT(*) > 1
ORDER BY seat_id;

-- name: ListOrphanReservationItemViolations :many
SELECT
    ri.id AS reservation_item_id,
    ri.reservation_id
FROM reservation_items AS ri
LEFT JOIN reservations AS r
    ON r.id = ri.reservation_id
WHERE r.id IS NULL
ORDER BY ri.id;

-- name: ListOrphanReservationSeatViolations :many
SELECT
    rs.id AS reservation_seat_id,
    rs.reservation_id
FROM reservation_seats AS rs
LEFT JOIN reservations AS r
    ON r.id = rs.reservation_id
WHERE r.id IS NULL
ORDER BY rs.id;

-- name: ListStaleExpiredActiveReservationViolations :many
SELECT
    id,
    status,
    expires_at
FROM reservations
WHERE status = 'reserved'
  AND expires_at < $1
ORDER BY expires_at, id;
