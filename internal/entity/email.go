package entity

// EmailMessage describes a transactional email.
type EmailMessage struct {
	To      string
	Subject string
	HTML    string
	Text    string
}
