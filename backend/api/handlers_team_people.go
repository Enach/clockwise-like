package api

// PAC-129 (spec docs/specs/PAC-129.md; contract contracts/features/PAC-129.md
// §3.1, §3.2): addTeamPerson and removeTeamPerson, implemented through the
// generated StrictServerInterface (factory §4), and the TeamPerson shape
// shared by getTeam, addTeamPerson and inviteTeamMember.

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Enach/paceday/backend/api/gen"
	"github.com/Enach/paceday/backend/auth"
	"github.com/Enach/paceday/backend/storage"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// addTeamPerson mounts the generated wrapper; the team id is parsed here so a
// malformed one is the contract's 400 "invalid team id".
func (h *teamHandlers) addTeamPerson(w http.ResponseWriter, r *http.Request) {
	teamID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, "invalid team id", http.StatusBadRequest)
		return
	}
	newGeneratedHandler(h.db, nil).AddTeamPerson(w, r, teamID)
}

// removeTeamPerson mounts the generated wrapper; the {email} segment is
// path-unescaped here and normalised by the handler.
func (h *teamHandlers) removeTeamPerson(w http.ResponseWriter, r *http.Request) {
	teamID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, "invalid team id", http.StatusBadRequest)
		return
	}
	newGeneratedHandler(h.db, nil).RemoveTeamPerson(w, r, teamID, openapi_types.Email(managerEmailParam(r)))
}

// AddTeamPerson is POST /api/teams/{id}/people (contract §3.1).
func (s *generatedServer) AddTeamPerson(ctx context.Context, req gen.AddTeamPersonRequestObject) (gen.AddTeamPersonResponseObject, error) {
	caller := auth.UserIDFromContext(ctx)
	if caller == uuid.Nil {
		return gen.AddTeamPerson401JSONResponse{UnauthorizedJSONResponse: gen.UnauthorizedJSONResponse{Error: "unauthorized"}}, nil
	}
	raw := ""
	name := ""
	if req.Body != nil {
		raw = req.Body.Email
		if req.Body.Name != nil {
			name = *req.Body.Name
		}
	}
	if storage.TrimAddressSpace(raw) == "" {
		return gen.AddTeamPerson400JSONResponse{Error: "email is required"}, nil
	}
	email, err := normalizeManagerEmail(raw)
	if err != nil {
		return gen.AddTeamPerson422JSONResponse{Error: gen.AddTeamPersonInvalidEmail}, nil
	}
	person, created, err := storage.AddTeamPersonCtx(ctx, s.db, req.Id, caller, email, name)
	switch {
	case errors.Is(err, storage.ErrNotTeamOwner), errors.Is(err, storage.ErrTeamGone):
		return gen.AddTeamPerson403JSONResponse{JsonForbiddenJSONResponse: gen.JsonForbiddenJSONResponse{Error: "forbidden"}}, nil
	case errors.Is(err, storage.ErrOwnAddress):
		return gen.AddTeamPerson422JSONResponse{Error: gen.AddTeamPersonOwnAddress}, nil
	case errors.Is(err, storage.ErrPacedayMemberAddress):
		return gen.AddTeamPerson422JSONResponse{Error: gen.AddTeamPersonPacedayMember}, nil
	case errors.Is(err, storage.ErrTeamMemberLimit):
		return gen.AddTeamPerson422JSONResponse{Error: gen.AddTeamPersonLimitReached}, nil
	case err != nil:
		return gen.AddTeamPerson500JSONResponse{ServerErrorJSONResponse: gen.ServerErrorJSONResponse{Error: serverFailure(ctx, "addTeamPerson", err, http.StatusInternalServerError)}}, nil
	}
	body := addedPerson(person, person.PendingExpiry)
	if created {
		return gen.AddTeamPerson201JSONResponse(body), nil
	}
	return gen.AddTeamPerson200JSONResponse(body), nil
}

// RemoveTeamPerson is DELETE /api/teams/{id}/people/{email} (contract §3.2).
func (s *generatedServer) RemoveTeamPerson(ctx context.Context, req gen.RemoveTeamPersonRequestObject) (gen.RemoveTeamPersonResponseObject, error) {
	caller := auth.UserIDFromContext(ctx)
	if caller == uuid.Nil {
		return gen.RemoveTeamPerson401JSONResponse{UnauthorizedJSONResponse: gen.UnauthorizedJSONResponse{Error: "unauthorized"}}, nil
	}
	email, err := normalizeManagerEmail(string(req.Email))
	if err != nil {
		return gen.RemoveTeamPerson400JSONResponse{Error: "invalid email"}, nil
	}
	err = storage.RemoveTeamPersonCtx(ctx, s.db, req.Id, caller, email)
	switch {
	case err == nil:
		return gen.RemoveTeamPerson204Response{}, nil
	case errors.Is(err, storage.ErrNotTeamOwner), errors.Is(err, storage.ErrTeamGone):
		return gen.RemoveTeamPerson403JSONResponse{JsonForbiddenJSONResponse: gen.JsonForbiddenJSONResponse{Error: "forbidden"}}, nil
	case errors.Is(err, storage.ErrTeamPersonNotFound):
		return gen.RemoveTeamPerson404JSONResponse{Error: gen.TeamPersonNotFound}, nil
	case errors.Is(err, storage.ErrLastOwner):
		return gen.RemoveTeamPerson422JSONResponse{Error: storage.ErrLastOwner.Error()}, nil
	}
	return gen.RemoveTeamPerson500JSONResponse{ServerErrorJSONResponse: gen.ServerErrorJSONResponse{Error: serverFailure(ctx, "removeTeamPerson", err, http.StatusInternalServerError)}}, nil
}

// addedPerson is an added person's TeamPerson; expiry is its pending
// invitation's, shown only to owners (the caller decides, contract m-6).
func addedPerson(a *storage.AddedMember, expiry *time.Time) gen.TeamPerson {
	p := gen.TeamPerson{Email: a.Email, Name: a.DisplayName, Kind: gen.TeamPersonKindAdded}
	if expiry != nil {
		p.Invitation = &gen.TeamPersonInvitation{ExpiresAt: *expiry}
	}
	return p
}

// pacedayPerson is a Paceday member's TeamPerson.
func pacedayPerson(m *storage.TeamMember) gen.TeamPerson {
	id := m.UserID
	role := gen.TeamPersonRole(m.Role)
	return gen.TeamPerson{
		Email:  strings.ToLower(m.Email),
		Name:   m.Name,
		Kind:   gen.TeamPersonKindPaceday,
		UserId: &id,
		Role:   &role,
	}
}

// teamPeople is TeamDetail.people: the Paceday members in members order, then
// the added people by the time they were added. Invitations are shown only
// when withInvitations (the caller is an owner).
func teamPeople(members []*storage.TeamMember, added []*storage.AddedMember, pending map[string]time.Time, withInvitations bool) []gen.TeamPerson {
	out := make([]gen.TeamPerson, 0, len(members)+len(added))
	for _, m := range members {
		out = append(out, pacedayPerson(m))
	}
	for _, a := range added {
		var expiry *time.Time
		if withInvitations {
			if t, ok := pending[a.Email]; ok {
				expiry = &t
			}
		}
		out = append(out, addedPerson(a, expiry))
	}
	return out
}
