package entity

// PushNotification is a single push delivery targeted at one or more users by their OneSignal
// external_id alias (the backend user UUID). Headings and Contents are keyed by language code
// (e.g. "en", "id", "ar"); "en" should always be present as the OneSignal default language. Data
// carries optional key/value extras such as a deep_link the app consumes when the push is tapped.
type PushNotification struct {
	ExternalIDs []string
	Headings    map[string]string
	Contents    map[string]string
	Data        map[string]string
}

// ReminderCandidate is one user eligible for a streak/daily reading reminder, resolved in the
// user's local timezone. LocalDate (YYYY-MM-DD) is the user's current local date, used to scope the
// per-day send cooldown so a restart can't double-send.
type ReminderCandidate struct {
	UserID    string
	Lang      string
	LocalDate string
}
