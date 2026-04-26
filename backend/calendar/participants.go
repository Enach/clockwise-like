package calendar

// ParticipantInfo holds resolved timezone and working-hours info for a meeting participant.
// Timezone/hour resolution from external calendars is not yet available;
// empty fields are handled gracefully by the scheduling prompt.
type ParticipantInfo struct {
	Email     string `json:"email"`
	Timezone  string `json:"timezone"`  // empty = unknown
	WorkStart string `json:"workStart"` // "09:00" or empty
	WorkEnd   string `json:"workEnd"`   // "18:00" or empty
}

// ResolveParticipants returns basic info for each participant email.
// Future enhancement: query Google Calendar settings API to fetch actual timezones.
func ResolveParticipants(emails []string) []ParticipantInfo {
	infos := make([]ParticipantInfo, len(emails))
	for i, email := range emails {
		infos[i] = ParticipantInfo{Email: email}
	}
	return infos
}
