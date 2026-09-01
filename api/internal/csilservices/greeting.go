package csilservices

import (
	"context"
	"errors"
	"strings"

	"github.com/catalystcommunity/tinku/api/internal/csil"
	"github.com/catalystcommunity/tinku/api/internal/reqctx"
	"github.com/catalystcommunity/tinku/api/internal/store"
)

// maxGreetingLength mirrors the `.size (1..280)` constraint on
// CreateGreetingRequest.message in csil/types/greetings.csil. The generated
// validation is available to callers; the server re-checks because a server
// never trusts a client to have run the client-side half of a contract.
const maxGreetingLength = 280

// GreetingService implements csil.GreetingService — the hello-world domain.
// Reads are anonymous; writing needs a session.
type GreetingService struct {
	Store store.Store
}

var _ csil.GreetingService = (*GreetingService)(nil)

// ListGreetings returns every greeting, newest first.
func (s *GreetingService) ListGreetings(ctx context.Context, _ csil.Empty) (csil.GreetingList, error) {
	greetings, err := s.Store.ListGreetings(ctx)
	if err != nil {
		return csil.GreetingList{}, err
	}
	out := make([]csil.Greeting, 0, len(greetings))
	for i := range greetings {
		out = append(out, toGreeting(&greetings[i]))
	}
	return csil.GreetingList{Greetings: out}, nil
}

// GetGreeting returns one greeting by id.
func (s *GreetingService) GetGreeting(ctx context.Context, req csil.GetGreetingRequest) (csil.Greeting, error) {
	greeting, err := s.Store.GreetingByID(ctx, string(req.Id))
	if errors.Is(err, store.ErrNotFound) {
		return csil.Greeting{}, NotFound("greeting", "no greeting with that id")
	}
	if err != nil {
		return csil.Greeting{}, err
	}
	return toGreeting(greeting), nil
}

// CreateGreeting stores a greeting attributed to the caller.
func (s *GreetingService) CreateGreeting(ctx context.Context, req csil.CreateGreetingRequest) (csil.Greeting, error) {
	user, ok := reqctx.User(ctx)
	if !ok {
		return csil.Greeting{}, Unauthenticated("log in to leave a greeting")
	}

	message := strings.TrimSpace(req.Message)
	if message == "" {
		return csil.Greeting{}, Invalid("message", "a greeting cannot be empty")
	}
	// Counted in runes, not bytes: the schema's `.size (1..280)` is a
	// character count, and a byte count would reject a shorter message in
	// any language that needs more than one byte per character.
	if len([]rune(message)) > maxGreetingLength {
		return csil.Greeting{}, Invalid("message", "a greeting is at most 280 characters")
	}

	greeting, err := s.Store.CreateGreeting(ctx, user.ID, message)
	if err != nil {
		return csil.Greeting{}, err
	}
	return toGreeting(greeting), nil
}

func toGreeting(g *store.Greeting) csil.Greeting {
	out := csil.Greeting{
		Id:           csil.GreetingID(g.ID),
		AuthorHandle: g.AuthorHandle,
		Message:      g.Message,
		CreatedAt:    g.CreatedAt,
	}
	// The generated id types are defined types over string, so a *string
	// from the store cannot be assigned to a *csil.UserID directly — the
	// pointed-at value has to be converted and re-addressed.
	if g.AuthorID != nil {
		authorID := csil.UserID(*g.AuthorID)
		out.AuthorId = &authorID
	}
	return out
}
