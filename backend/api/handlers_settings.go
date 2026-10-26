package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/Enach/paceday/backend/nlp"
	"github.com/Enach/paceday/backend/storage"
	"github.com/robfig/cron/v3"
)

var timePattern = regexp.MustCompile(`^\d{2}:\d{2}$`)

var validLLMProviders = map[string]bool{
	"openai":       true,
	"anthropic":    true,
	"ollama":       true,
	"bedrock":      true,
	"azure_openai": true,
	"vertex":       true,
	"":             true,
}

var validCalendarProviders = map[string]bool{"google": true, "outlook": true, "webcal": true}
var validConferencingProviders = map[string]bool{"meet": true, "zoom": true, "teams": true}
var validRecapSendTo = map[string]bool{"dm": true, "channel": true}
var validTimeFormats = map[string]bool{"12h": true, "24h": true}

type settingsHandlers struct {
	db *sql.DB
}

// getSettings returns the caller's settings, creating their row on first read
// (PAC-24). The provider key is never in the body; llmApiKeySet says whether
// one is on file.
func (h *settingsHandlers) getSettings(w http.ResponseWriter, r *http.Request) {
	s, err := storage.GetOrCreateSettingsByUser(h.db, userIDFromCtx(r.Context()))
	if err != nil {
		writeServerErrorText(w, r, "getSettings", err, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(s)
}

// settingsPatchProperties is every property PATCH /api/settings accepts: the
// JSON names of storage.Settings plus the write-only llmApiKey, which the
// struct never encodes.
var settingsPatchProperties = func() map[string]bool {
	props := map[string]bool{"llmApiKey": true}
	t := reflect.TypeOf(storage.Settings{})
	for i := 0; i < t.NumField(); i++ {
		name := strings.Split(t.Field(i).Tag.Get("json"), ",")[0]
		if name != "" && name != "-" {
			props[name] = true
		}
	}
	return props
}()

// patchSettings merges the body into the caller's own settings (PAC-24):
// an absent property is unchanged, a present one is set exactly, null and
// unknown properties are a 400, workingHours and lunchBreaks are replaced
// whole, and llmApiKeySet and updatedAt are accepted and ignored.
func (h *settingsHandlers) patchSettings(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromCtx(r.Context())
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		writeError(w, "invalid JSON: the body must be an object", http.StatusBadRequest)
		return
	}
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if !settingsPatchProperties[name] {
			writeError(w, "unknown property: "+name, http.StatusBadRequest)
			return
		}
		if bytes.Equal(bytes.TrimSpace(fields[name]), []byte("null")) {
			writeError(w, name+" must not be null", http.StatusBadRequest)
			return
		}
	}

	s, err := storage.GetOrCreateSettingsByUser(h.db, userID)
	if err != nil {
		writeServerErrorText(w, r, "patchSettings", err, http.StatusInternalServerError)
		return
	}
	stored, err := json.Marshal(s)
	if err != nil {
		writeServerErrorText(w, r, "patchSettings", err, http.StatusInternalServerError)
		return
	}
	// Decoding onto the stored record is the merge; maps would be merged into,
	// so the two schedule properties are emptied first to be replaced whole.
	if _, ok := fields["workingHours"]; ok {
		s.WorkingHours = storage.WorkingHoursSchedule{}
	}
	if _, ok := fields["lunchBreaks"]; ok {
		s.LunchBreaks = storage.LunchBreakSchedule{}
	}
	if err := json.Unmarshal(raw, s); err != nil {
		writeError(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if key, ok := fields["llmApiKey"]; ok {
		if err := json.Unmarshal(key, &s.LLMAPIKey); err != nil {
			writeError(w, "llmApiKey must be a string", http.StatusBadRequest)
			return
		}
	}

	changed, err := changedSettingsProperties(stored, s)
	if err != nil {
		writeServerErrorText(w, r, "patchSettings", err, http.StatusInternalServerError)
		return
	}
	if err := validateChangedSettings(s, changed); err != nil {
		writeError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := storage.SaveSettingsForUser(h.db, userID, s); err != nil {
		writeServerErrorText(w, r, "patchSettings", err, http.StatusInternalServerError)
		return
	}
	updated, err := storage.GetOrCreateSettingsByUser(h.db, userID)
	if err != nil {
		writeServerErrorText(w, r, "patchSettings", err, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(updated)
}

// settingsProblem is one validation failure and the properties it is about.
type settingsProblem struct {
	props []string
	msg   string
}

// validateSettings returns the first problem with s, whatever it is about.
func validateSettings(s *storage.Settings) error {
	return validateChangedSettings(s, nil)
}

// changedSettingsProperties lists the properties whose encoded value differs
// between the stored document and the merged one. The write-only key is never
// validated, so it is not compared.
func changedSettingsProperties(stored []byte, merged *storage.Settings) (map[string]bool, error) {
	after, err := json.Marshal(merged)
	if err != nil {
		return nil, err
	}
	var a, b map[string]json.RawMessage
	if err := json.Unmarshal(stored, &a); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(after, &b); err != nil {
		return nil, err
	}
	changed := map[string]bool{}
	for k, v := range b {
		if !bytes.Equal(a[k], v) {
			changed[k] = true
		}
	}
	return changed, nil
}

// validateChangedSettings returns the first problem that concerns a property
// in changed, or any problem when changed is nil. PATCH validates only what the
// body changes (PAC-24, contract revision 3), so a stored value that a newer
// rule rejects never stops a user saving something else, and a document read
// from GET can always be submitted back unchanged.
func validateChangedSettings(s *storage.Settings, changed map[string]bool) error {
	for _, p := range settingsProblems(s) {
		if changed == nil {
			return &validationError{p.msg}
		}
		for _, prop := range p.props {
			if changed[prop] {
				return &validationError{p.msg}
			}
		}
	}
	return nil
}

func settingsProblems(s *storage.Settings) []settingsProblem {
	var out []settingsProblem
	add := func(msg string, props ...string) { out = append(out, settingsProblem{props, msg}) }

	for _, f := range []struct{ prop, val string }{
		{"workStart", s.WorkStart}, {"workEnd", s.WorkEnd},
		{"lunchStart", s.LunchStart}, {"lunchEnd", s.LunchEnd},
	} {
		if f.val != "" && !validClockTime(f.val) {
			add(f.prop+" must be in HH:MM format", f.prop)
		}
	}
	if validClockTime(s.WorkStart) && validClockTime(s.WorkEnd) && s.WorkEnd <= s.WorkStart {
		add("workEnd must be after workStart", "workStart", "workEnd")
	}
	if validClockTime(s.LunchStart) && validClockTime(s.LunchEnd) && s.LunchEnd <= s.LunchStart {
		add("lunchEnd must be after lunchStart", "lunchStart", "lunchEnd")
	}
	if _, err := time.LoadLocation(s.Timezone); err != nil {
		add("timezone is not a known IANA location", "timezone")
	}

	// Messages as catalogued in the contract's SettingsValidationError.
	for _, f := range []struct {
		prop, msg string
		val       int
	}{
		{"focusMinBlockMinutes", "must be positive", s.FocusMinBlockMinutes},
		{"focusMaxBlockMinutes", "must be positive", s.FocusMaxBlockMinutes},
		{"focusDailyTargetMinutes", "must be positive", s.FocusDailyTargetMinutes},
		{"outOfHoursMeetingsPerWeek", "must be zero or positive", s.OutOfHoursMeetingsPerWeek},
		{"bufferBeforeMinutes", "must be positive", s.BufferBeforeMinutes},
		{"bufferAfterMinutes", "must be positive", s.BufferAfterMinutes},
		{"bufferMinMeetingMinutes", "must be zero or positive", s.BufferMinMeetingMinutes},
	} {
		if f.val < 0 {
			add(f.prop+" "+f.msg, f.prop)
		}
	}
	if s.FocusMinBlockMinutes > s.FocusMaxBlockMinutes {
		add("focusMinBlockMinutes must not exceed focusMaxBlockMinutes", "focusMinBlockMinutes", "focusMaxBlockMinutes")
	}

	if s.AutoScheduleCron != "" {
		if len(strings.Fields(s.AutoScheduleCron)) != 5 {
			add("autoScheduleCron must be a 5-part cron expression", "autoScheduleCron")
		} else if _, err := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow).Parse(s.AutoScheduleCron); err != nil {
			add("autoScheduleCron is invalid: "+err.Error(), "autoScheduleCron")
		}
	}

	if !validLLMProviders[s.LLMProvider] {
		add("llmProvider must be one of: openai, anthropic, ollama, bedrock, azure_openai, vertex", "llmProvider")
	}
	// PAC-66: destinations of the ambient-credential providers, whichever
	// provider is selected. Only a changed value reaches the caller (PAC-24
	// AC-17), and "" is always accepted (spec AC-15).
	if msg := nlp.AzureEndpointWriteProblem(s.AzureEndpoint); msg != "" {
		add(msg, "azureEndpoint")
	}
	if msg := nlp.GCPLocationWriteProblem(s.GCPLocation); msg != "" {
		add(msg, "gcpLocation")
	}
	if msg := nlp.AWSRegionWriteProblem(s.AWSRegion); msg != "" {
		add(msg, "awsRegion")
	}
	if !validCalendarProviders[s.CalendarProvider] {
		add("calendarProvider must be one of: google, outlook, webcal", "calendarProvider")
	}
	if !validConferencingProviders[s.ConferencingProvider] {
		add("conferencingProvider must be one of: meet, zoom, teams", "conferencingProvider")
	}
	if !validRecapSendTo[s.RecapSendTo] {
		add("recapSendTo must be dm or channel", "recapSendTo")
	}
	if !validClockTime(s.RecapSendTime) {
		add("recapSendTime must be in HH:MM format", "recapSendTime")
	}
	if !validTimeFormats[s.TimeFormat] {
		add("timeFormat must be 12h or 24h", "timeFormat")
	}

	if err := validateWorkingHours(s); err != nil {
		add(err.Error(), "workingHours")
	}
	if err := validateLunchBreaks(s); err != nil {
		add(err.Error(), "lunchBreaks")
	}
	return out
}

var validScheduleWeekdays = map[string]bool{
	"monday": true, "tuesday": true, "wednesday": true, "thursday": true,
	"friday": true, "saturday": true, "sunday": true,
}

// validateWorkingHours compares mode and weekday keys exactly (PAC-24): a
// "BY_DAY" mode or a "Monday" key used to pass after lower-casing, be stored,
// and never match WorkWindow's lowercase lookup.
func validateWorkingHours(s *storage.Settings) error {
	mode := s.WorkingHours.Mode
	if mode != "" && mode != "all_days" && mode != "by_day" {
		return &validationError{"workingHours.mode must be all_days or by_day"}
	}
	if mode == "" && len(s.WorkingHours.Days) > 0 {
		return &validationError{"workingHours.mode is required when day-specific hours are provided"}
	}
	// default and days are checked whatever the mode: either can be stored
	// and returned, and a GET must satisfy the contract (PAC-24 revision 4).
	if scheduleHasValues(s.WorkingHours.Default) {
		if err := validateDaySchedule("workingHours.default", s.WorkingHours.Default); err != nil {
			return err
		}
	}
	if mode == "by_day" && len(s.WorkingHours.Days) == 0 {
		return &validationError{"workingHours.days must contain at least one weekday when mode is by_day"}
	}
	for day, schedule := range s.WorkingHours.Days {
		if !validScheduleWeekdays[day] {
			return &validationError{"workingHours.days." + day + " is not a valid weekday"}
		}
		if err := validateDaySchedule("workingHours.days."+day, schedule); err != nil {
			return err
		}
	}
	return nil
}

func validateLunchBreaks(s *storage.Settings) error {
	for day, schedule := range s.LunchBreaks {
		if !validScheduleWeekdays[day] {
			return &validationError{"lunchBreaks." + day + " is not a valid weekday"}
		}
		if err := validateDaySchedule("lunchBreaks."+day, schedule); err != nil {
			return err
		}
	}
	return nil
}

func scheduleHasValues(s storage.DaySchedule) bool {
	return s.Enabled || s.Start != "" || s.End != ""
}

func validateDaySchedule(field string, schedule storage.DaySchedule) error {
	if !schedule.Enabled {
		if schedule.Start != "" && !validClockTime(schedule.Start) {
			return &validationError{field + ".start must be in HH:MM format"}
		}
		if schedule.End != "" && !validClockTime(schedule.End) {
			return &validationError{field + ".end must be in HH:MM format"}
		}
		return nil
	}
	if schedule.Start == "" || schedule.End == "" {
		return &validationError{field + " requires start and end when enabled"}
	}
	if !validClockTime(schedule.Start) || !validClockTime(schedule.End) {
		return &validationError{field + " start and end must be in HH:MM format"}
	}
	start, _ := time.Parse("15:04", schedule.Start)
	end, _ := time.Parse("15:04", schedule.End)
	if !end.After(start) {
		return &validationError{field + " end must be after start"}
	}
	return nil
}

func validClockTime(value string) bool {
	if !timePattern.MatchString(value) {
		return false
	}
	_, err := time.Parse("15:04", value)
	return err == nil
}

type validationError struct {
	msg string
}

func (e *validationError) Error() string { return e.msg }

func writeError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
