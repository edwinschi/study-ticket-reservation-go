DROP INDEX IF EXISTS uq_active_reservation_seat;
DROP INDEX IF EXISTS ix_reservation_seats_seat_id_status;
DROP TABLE IF EXISTS reservation_seats;

DROP INDEX IF EXISTS ix_reservation_items_ticket_type_id_status;
DROP TABLE IF EXISTS reservation_items;

DROP INDEX IF EXISTS ix_reservations_status_expires_at;
DROP INDEX IF EXISTS ix_reservations_user_id;
DROP INDEX IF EXISTS ix_reservations_visitor_session_id;
DROP TABLE IF EXISTS reservations;

DROP INDEX IF EXISTS ix_seats_event_id;
DROP TABLE IF EXISTS seats;

DROP INDEX IF EXISTS ix_ticket_types_event_id;
DROP TABLE IF EXISTS ticket_types;

DROP TABLE IF EXISTS visitor_sessions;
DROP TABLE IF EXISTS events;
DROP TABLE IF EXISTS users;

DROP EXTENSION IF EXISTS pgcrypto;

