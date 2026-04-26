package storage

import (
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"
)

func insertTestUser(t *testing.T, db *sql.DB, email, name string) uuid.UUID {
	t.Helper()
	u, err := UpsertUser(db, email, name, "", "google", email)
	if err != nil {
		t.Fatalf("insertTestUser(%q): %v", email, err)
	}
	return u.ID
}

func TestCreateAndGetSchedulingLink(t *testing.T) {
	db := openTestDB(t)
	ownerID := insertTestUser(t, db, "owner@test.com", "Owner User")

	link, err := CreateSchedulingLink(db, &SchedulingLink{
		OwnerUserID:     ownerID,
		Slug:            "owner-30min",
		Title:           "Quick Chat",
		DurationOptions: []int{30, 60},
		DaysOfWeek:      []int{1, 2, 3, 4, 5},
		WindowStart:     "09:00",
		WindowEnd:       "17:00",
	})
	if err != nil {
		t.Fatalf("CreateSchedulingLink: %v", err)
	}
	if link == nil {
		t.Fatal("expected link, got nil")
		return
	}
	if link.Slug != "owner-30min" {
		t.Errorf("Slug = %q, want owner-30min", link.Slug)
	}

	got, err := GetSchedulingLinkBySlug(db, "owner-30min")
	if err != nil {
		t.Fatalf("GetSchedulingLinkBySlug: %v", err)
	}
	if got == nil {
		t.Fatal("expected link from DB, got nil")
		return
	}
	if got.Title != "Quick Chat" {
		t.Errorf("Title = %q, want Quick Chat", got.Title)
	}
	if len(got.DurationOptions) != 2 {
		t.Errorf("DurationOptions len = %d, want 2", len(got.DurationOptions))
	}
}

func TestGetSchedulingLinkBySlug_NotFound(t *testing.T) {
	db := openTestDB(t)
	got, err := GetSchedulingLinkBySlug(db, "nonexistent-slug")
	if err != nil {
		t.Fatalf("GetSchedulingLinkBySlug: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil, got %+v", got)
	}
}

func TestUpdateSchedulingLink(t *testing.T) {
	db := openTestDB(t)
	ownerID := insertTestUser(t, db, "edit@test.com", "Edit User")

	link, err := CreateSchedulingLink(db, &SchedulingLink{
		OwnerUserID:     ownerID,
		Slug:            "edit-30min",
		Title:           "Original",
		DurationOptions: []int{30},
		DaysOfWeek:      []int{1, 2, 3, 4, 5},
		WindowStart:     "09:00",
		WindowEnd:       "17:00",
	})
	if err != nil {
		t.Fatalf("CreateSchedulingLink: %v", err)
	}
	if link == nil {
		t.Fatal("expected link")
		return
	}

	updated, err := UpdateSchedulingLink(db, link.ID, &SchedulingLink{
		Title:           "Updated Title",
		DurationOptions: []int{15, 30, 60},
		DaysOfWeek:      []int{1, 2, 3},
		WindowStart:     "10:00",
		WindowEnd:       "18:00",
		Active:          true,
	})
	if err != nil {
		t.Fatalf("UpdateSchedulingLink: %v", err)
	}
	if updated == nil {
		t.Fatal("expected updated link")
		return
	}
	if updated.Title != "Updated Title" {
		t.Errorf("Title = %q, want Updated Title", updated.Title)
	}
	if len(updated.DurationOptions) != 3 {
		t.Errorf("DurationOptions len = %d, want 3", len(updated.DurationOptions))
	}
}

func TestAddAndGetLinkHosts(t *testing.T) {
	db := openTestDB(t)
	ownerID := insertTestUser(t, db, "host-owner@test.com", "Host Owner")
	coHostID := insertTestUser(t, db, "cohost@test.com", "Co Host")

	link, err := CreateSchedulingLink(db, &SchedulingLink{
		OwnerUserID:     ownerID,
		Slug:            "host-30min",
		Title:           "Host Test",
		DurationOptions: []int{30},
		DaysOfWeek:      []int{1, 2, 3, 4, 5},
		WindowStart:     "09:00",
		WindowEnd:       "17:00",
	})
	if err != nil {
		t.Fatalf("CreateSchedulingLink: %v", err)
	}
	if link == nil {
		t.Fatal("expected link")
		return
	}

	if _, err = AddLinkHost(db, link.ID, ownerID, "accepted"); err != nil {
		t.Fatalf("AddLinkHost owner: %v", err)
	}
	if _, err = AddLinkHost(db, link.ID, coHostID, "pending"); err != nil {
		t.Fatalf("AddLinkHost cohost: %v", err)
	}

	accepted, err := GetAcceptedHosts(db, link.ID)
	if err != nil {
		t.Fatalf("GetAcceptedHosts: %v", err)
	}
	if len(accepted) != 1 {
		t.Errorf("accepted hosts = %d, want 1", len(accepted))
	}

	pending, err := GetPendingInvitesForUser(db, coHostID)
	if err != nil {
		t.Fatalf("GetPendingInvitesForUser: %v", err)
	}
	if len(pending) != 1 {
		t.Errorf("pending invites = %d, want 1", len(pending))
	}

	if err = RespondToHostInvite(db, link.ID, coHostID, "accepted"); err != nil {
		t.Fatalf("RespondToHostInvite: %v", err)
	}

	accepted2, err := GetAcceptedHosts(db, link.ID)
	if err != nil {
		t.Fatalf("GetAcceptedHosts after accept: %v", err)
	}
	if len(accepted2) != 2 {
		t.Errorf("accepted hosts after accept = %d, want 2", len(accepted2))
	}
}

func TestCreateBooking(t *testing.T) {
	db := openTestDB(t)
	ownerID := insertTestUser(t, db, "booking-owner@test.com", "Booking Owner")

	link, err := CreateSchedulingLink(db, &SchedulingLink{
		OwnerUserID:     ownerID,
		Slug:            "booking-30min",
		Title:           "Book Me",
		DurationOptions: []int{30},
		DaysOfWeek:      []int{1, 2, 3, 4, 5},
		WindowStart:     "09:00",
		WindowEnd:       "17:00",
	})
	if err != nil {
		t.Fatalf("CreateSchedulingLink: %v", err)
	}
	if link == nil {
		t.Fatal("expected link")
		return
	}

	start := time.Date(2024, 6, 15, 14, 0, 0, 0, time.UTC)
	end := start.Add(30 * time.Minute)

	booking, err := CreateBooking(db, &Booking{
		LinkID:      link.ID,
		BookerName:  "Alice Booker",
		BookerEmail: "alice@example.com",
		StartTime:   start,
		EndTime:     end,
		Notes:       "Looking forward to it",
	})
	if err != nil {
		t.Fatalf("CreateBooking: %v", err)
	}
	if booking == nil {
		t.Fatal("expected booking")
		return
	}
	if booking.BookerName != "Alice Booker" {
		t.Errorf("BookerName = %q, want Alice Booker", booking.BookerName)
	}
	if booking.Status != "confirmed" {
		t.Errorf("Status = %q, want confirmed", booking.Status)
	}

	bookings, err := GetBookingsByLink(db, link.ID)
	if err != nil {
		t.Fatalf("GetBookingsByLink: %v", err)
	}
	if len(bookings) != 1 {
		t.Errorf("bookings = %d, want 1", len(bookings))
	}
}

func TestListSchedulingLinksByUser(t *testing.T) {
	db := openTestDB(t)
	ownerID := insertTestUser(t, db, "list-owner@test.com", "List Owner")

	for i, slug := range []string{"list-a-30min", "list-b-60min"} {
		_, err := CreateSchedulingLink(db, &SchedulingLink{
			OwnerUserID:     ownerID,
			Slug:            slug,
			Title:           slug,
			DurationOptions: []int{30 * (i + 1)},
			DaysOfWeek:      []int{1, 2, 3, 4, 5},
			WindowStart:     "09:00",
			WindowEnd:       "17:00",
		})
		if err != nil {
			t.Fatalf("CreateSchedulingLink %d: %v", i, err)
		}
	}

	links, err := ListSchedulingLinksByUser(db, ownerID)
	if err != nil {
		t.Fatalf("ListSchedulingLinksByUser: %v", err)
	}
	if len(links) != 2 {
		t.Errorf("links = %d, want 2", len(links))
	}
}

func TestSlugExists(t *testing.T) {
	db := openTestDB(t)
	ownerID := insertTestUser(t, db, "slug-check@test.com", "Slug Check")

	_, err := CreateSchedulingLink(db, &SchedulingLink{
		OwnerUserID:     ownerID,
		Slug:            "my-unique-slug",
		Title:           "Slug Test",
		DurationOptions: []int{30},
		DaysOfWeek:      []int{1, 2, 3, 4, 5},
		WindowStart:     "09:00",
		WindowEnd:       "17:00",
	})
	if err != nil {
		t.Fatalf("CreateSchedulingLink: %v", err)
	}

	exists, err := SlugExists(db, "my-unique-slug")
	if err != nil {
		t.Fatalf("SlugExists: %v", err)
	}
	if !exists {
		t.Error("expected slug to exist")
	}

	notExists, err := SlugExists(db, "no-such-slug")
	if err != nil {
		t.Fatalf("SlugExists: %v", err)
	}
	if notExists {
		t.Error("expected slug to not exist")
	}
}
