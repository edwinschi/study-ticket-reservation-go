package db

import (
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

func ParseUUID(value string) (pgtype.UUID, error) {
	var id pgtype.UUID
	if err := id.Scan(value); err != nil {
		return pgtype.UUID{}, fmt.Errorf("parse uuid: %w", err)
	}
	return id, nil
}

func UUIDToString(id pgtype.UUID) string {
	if !id.Valid {
		return ""
	}

	// pgtype.UUID stores raw bytes. Formatting the byte groups explicitly keeps DTOs readable
	// without forcing repository code to depend on a separate UUID library.
	return fmt.Sprintf(
		"%x-%x-%x-%x-%x",
		id.Bytes[0:4],
		id.Bytes[4:6],
		id.Bytes[6:8],
		id.Bytes[8:10],
		id.Bytes[10:16],
	)
}

func OptionalUUIDToString(id pgtype.UUID) *string {
	if !id.Valid {
		return nil
	}

	value := UUIDToString(id)
	return &value
}

func TimestamptzToTime(value pgtype.Timestamptz) time.Time {
	if !value.Valid {
		return time.Time{}
	}
	return value.Time
}
