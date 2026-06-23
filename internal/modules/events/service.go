package events

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"ticket-reservation-go-lab/internal/db"
	"ticket-reservation-go-lab/internal/sqlc"
)

var (
	ErrInvalidEventInput      = errors.New("invalid event input")
	ErrInvalidTicketTypeInput = errors.New("invalid ticket type input")
	ErrInvalidSeatInput       = errors.New("invalid seat input")
	ErrEventNotFound          = errors.New("event not found")
)

type Service struct {
	repository *Repository
}

func NewService(repository *Repository) *Service {
	return &Service{repository: repository}
}

func (s *Service) CreateEvent(ctx context.Context, input CreateEventRequest) (EventResponse, error) {
	if strings.TrimSpace(input.Name) == "" || input.StartsAt.IsZero() || input.EndsAt.IsZero() {
		return EventResponse{}, ErrInvalidEventInput
	}
	if !input.EndsAt.After(input.StartsAt) {
		return EventResponse{}, ErrInvalidEventInput
	}

	event, err := s.repository.CreateEvent(
		ctx,
		strings.TrimSpace(input.Name),
		pgtype.Timestamptz{Time: input.StartsAt.UTC(), Valid: true},
		pgtype.Timestamptz{Time: input.EndsAt.UTC(), Valid: true},
	)
	if err != nil {
		return EventResponse{}, fmt.Errorf("create event: %w", err)
	}
	return ToEventResponse(event), nil
}

func (s *Service) GetEvent(ctx context.Context, eventID pgtype.UUID) (EventResponse, error) {
	event, err := s.repository.GetEvent(ctx, eventID)
	if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, sql.ErrNoRows) {
		return EventResponse{}, ErrEventNotFound
	}
	if err != nil {
		return EventResponse{}, fmt.Errorf("get event: %w", err)
	}
	return ToEventResponse(event), nil
}

func (s *Service) CreateTicketType(
	ctx context.Context,
	eventID pgtype.UUID,
	input CreateTicketTypeRequest,
) (TicketTypeResponse, error) {
	if strings.TrimSpace(input.Name) == "" || input.TotalQuantity < 0 {
		return TicketTypeResponse{}, ErrInvalidTicketTypeInput
	}

	if _, err := s.repository.GetEvent(ctx, eventID); errors.Is(err, pgx.ErrNoRows) || errors.Is(err, sql.ErrNoRows) {
		return TicketTypeResponse{}, ErrEventNotFound
	} else if err != nil {
		return TicketTypeResponse{}, fmt.Errorf("get event: %w", err)
	}

	ticketType, err := s.repository.CreateTicketType(
		ctx,
		eventID,
		strings.TrimSpace(input.Name),
		input.TotalQuantity,
	)
	if err != nil {
		return TicketTypeResponse{}, fmt.Errorf("create ticket type: %w", err)
	}
	return ToTicketTypeResponse(ticketType), nil
}

func (s *Service) CreateSeats(
	ctx context.Context,
	eventID pgtype.UUID,
	input CreateSeatsRequest,
) ([]SeatResponse, error) {
	if len(input.Seats) == 0 {
		return nil, ErrInvalidSeatInput
	}

	if _, err := s.repository.GetEvent(ctx, eventID); errors.Is(err, pgx.ErrNoRows) || errors.Is(err, sql.ErrNoRows) {
		return nil, ErrEventNotFound
	} else if err != nil {
		return nil, fmt.Errorf("get event: %w", err)
	}

	seats := make([]SeatResponse, 0, len(input.Seats))
	for _, seatInput := range input.Seats {
		if strings.TrimSpace(seatInput.Section) == "" ||
			strings.TrimSpace(seatInput.RowName) == "" ||
			strings.TrimSpace(seatInput.SeatNumber) == "" {
			return nil, ErrInvalidSeatInput
		}
		created, err := s.repository.CreateSeat(ctx, eventID, CreateSeatRequest{
			Section:    strings.TrimSpace(seatInput.Section),
			RowName:    strings.TrimSpace(seatInput.RowName),
			SeatNumber: strings.TrimSpace(seatInput.SeatNumber),
		})
		if err != nil {
			return nil, fmt.Errorf("create seat: %w", err)
		}
		seats = append(seats, ToSeatResponse(created))
	}
	return seats, nil
}

func (s *Service) GetInventory(ctx context.Context, eventID pgtype.UUID) (InventoryResponse, error) {
	if _, err := s.repository.GetEvent(ctx, eventID); errors.Is(err, pgx.ErrNoRows) || errors.Is(err, sql.ErrNoRows) {
		return InventoryResponse{}, ErrEventNotFound
	} else if err != nil {
		return InventoryResponse{}, fmt.Errorf("get event: %w", err)
	}

	ticketTypes, err := s.repository.ListTicketTypeInventory(ctx, eventID)
	if err != nil {
		return InventoryResponse{}, fmt.Errorf("list ticket inventory: %w", err)
	}
	seats, err := s.repository.ListSeatInventory(ctx, eventID)
	if err != nil {
		return InventoryResponse{}, fmt.Errorf("list seat inventory: %w", err)
	}

	return InventoryResponse{
		TicketTypes: ToTicketTypeInventoryResponses(ticketTypes),
		Seats:       ToSeatInventoryResponses(seats),
	}, nil
}

func ToEventResponse(event sqlc.Event) EventResponse {
	return EventResponse{
		ID:        db.UUIDToString(event.ID),
		Name:      event.Name,
		StartsAt:  db.TimestamptzToTime(event.StartsAt),
		EndsAt:    db.TimestamptzToTime(event.EndsAt),
		CreatedAt: db.TimestamptzToTime(event.CreatedAt),
		UpdatedAt: db.TimestamptzToTime(event.UpdatedAt),
	}
}

func ToTicketTypeResponse(ticketType sqlc.TicketType) TicketTypeResponse {
	return TicketTypeResponse{
		ID:               db.UUIDToString(ticketType.ID),
		EventID:          db.UUIDToString(ticketType.EventID),
		Name:             ticketType.Name,
		TotalQuantity:    ticketType.TotalQuantity,
		SoldQuantity:     ticketType.SoldQuantity,
		ReservedQuantity: ticketType.ReservedQuantity,
		CreatedAt:        db.TimestamptzToTime(ticketType.CreatedAt),
		UpdatedAt:        db.TimestamptzToTime(ticketType.UpdatedAt),
	}
}

func ToSeatResponse(seat sqlc.Seat) SeatResponse {
	return SeatResponse{
		ID:         db.UUIDToString(seat.ID),
		EventID:    db.UUIDToString(seat.EventID),
		Section:    seat.Section,
		RowName:    seat.RowName,
		SeatNumber: seat.SeatNumber,
		CreatedAt:  db.TimestamptzToTime(seat.CreatedAt),
		UpdatedAt:  db.TimestamptzToTime(seat.UpdatedAt),
	}
}

func ToTicketTypeInventoryResponses(
	rows []sqlc.ListTicketTypeInventoryByEventRow,
) []TicketTypeInventoryResponse {
	responses := make([]TicketTypeInventoryResponse, 0, len(rows))
	for _, row := range rows {
		responses = append(responses, TicketTypeInventoryResponse{
			ID:                db.UUIDToString(row.ID),
			EventID:           db.UUIDToString(row.EventID),
			Name:              row.Name,
			TotalQuantity:     row.TotalQuantity,
			SoldQuantity:      row.SoldQuantity,
			ReservedQuantity:  row.ReservedQuantity,
			AvailableQuantity: row.AvailableQuantity,
		})
	}
	return responses
}

func ToSeatInventoryResponses(rows []sqlc.ListSeatInventoryByEventRow) []SeatInventoryResponse {
	responses := make([]SeatInventoryResponse, 0, len(rows))
	for _, row := range rows {
		responses = append(responses, SeatInventoryResponse{
			ID:         db.UUIDToString(row.ID),
			EventID:    db.UUIDToString(row.EventID),
			Section:    row.Section,
			RowName:    row.RowName,
			SeatNumber: row.SeatNumber,
			Status:     row.Status,
		})
	}
	return responses
}
