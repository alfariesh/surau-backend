package request

// Register -.
type Register struct {
	DisplayName string `json:"display_name" validate:"omitempty,max=255" example:"John Doe"`
	Name        string `json:"name"         validate:"omitempty,max=255" example:"John Doe"`
	Username    string `json:"username"     validate:"omitempty,max=255" example:"John Doe"`
	Email       string `json:"email"        validate:"required,email"    example:"john@example.com"`
	Password    string `json:"password"     validate:"required,min=8,max=72" example:"secret123"`
} // @name v1.Register

// Login -.
type Login struct {
	Email    string `json:"email"    validate:"required,email"        example:"john@example.com"`
	Password string `json:"password" validate:"required,min=8,max=72" example:"secret123"`
} // @name v1.Login

// VerifyEmail -.
type VerifyEmail struct {
	Token string `json:"token" validate:"required" example:"G66NnGZnWg4W88qGz3p9N0-GxjKuxEOHHsvWv3kBaBA"`
} // @name v1.VerifyEmail

// ResendVerification -.
type ResendVerification struct {
	Email string `json:"email" validate:"required,email" example:"john@example.com"`
} // @name v1.ResendVerification

// ForgotPassword -.
type ForgotPassword struct {
	Email string `json:"email" validate:"required,email" example:"john@example.com"`
} // @name v1.ForgotPassword

// ResetPassword -.
type ResetPassword struct {
	Token    string `json:"token"    validate:"required"     example:"G66NnGZnWg4W88qGz3p9N0-GxjKuxEOHHsvWv3kBaBA"`
	Password string `json:"password" validate:"required,min=8,max=72" example:"newsecret123"`
} // @name v1.ResetPassword

// ChangePassword -.
type ChangePassword struct {
	CurrentPassword string `json:"current_password" validate:"required,min=8,max=72" example:"oldsecret123"`
	NewPassword     string `json:"new_password"     validate:"required,min=8,max=72" example:"newsecret123"`
} // @name v1.ChangePassword

// RequestEmailChange -.
type RequestEmailChange struct {
	CurrentPassword string `json:"current_password" validate:"required,min=8,max=72" example:"secret123"`
	NewEmail        string `json:"new_email"        validate:"required,email"        example:"new@example.com"`
} // @name v1.RequestEmailChange

// VerifyEmailChange -.
type VerifyEmailChange struct {
	Token string `json:"token" validate:"required" example:"G66NnGZnWg4W88qGz3p9N0-GxjKuxEOHHsvWv3kBaBA"`
} // @name v1.VerifyEmailChange

// DeleteAccount -.
type DeleteAccount struct {
	CurrentPassword string `json:"current_password" validate:"required,min=8,max=72" example:"secret123"`
} // @name v1.DeleteAccount

// UserProfilePatch stores partial profile changes.
type UserProfilePatch struct {
	DisplayName            *string `json:"display_name"            validate:"omitempty,max=255" example:"John Doe"`
	Timezone               *string `json:"timezone"                validate:"omitempty,max=64"  example:"Asia/Jakarta"`
	CountryCode            *string `json:"country_code"            validate:"omitempty,max=2"   example:"ID"`
	PersonalizationEnabled *bool   `json:"personalization_enabled" example:"true"`
} // @name v1.UserProfilePatch

// UserOnboarding stores the first-run profile and preference answers.
type UserOnboarding struct {
	DisplayName              *string  `json:"display_name"                validate:"omitempty,max=255" example:"John"`
	Timezone                 *string  `json:"timezone"                    validate:"omitempty,max=64"  example:"Asia/Jakarta"`
	CountryCode              *string  `json:"country_code"                validate:"omitempty,len=2"   example:"ID"`
	PersonalizationEnabled   *bool    `json:"personalization_enabled"     example:"true"`
	PreferredUILang          string   `json:"preferred_ui_lang"           validate:"omitempty,max=16"  example:"id"`
	PreferredContentLang     string   `json:"preferred_content_lang"      validate:"omitempty,max=16"  example:"id"`
	FallbackLangs            []string `json:"fallback_langs"              validate:"omitempty,max=3,dive,max=16" example:"id,en"`
	ArabicLevel              string   `json:"arabic_level"                validate:"omitempty,max=32"  example:"basic"`
	ReaderMode               string   `json:"reader_mode"                 validate:"omitempty,max=64"  example:"arabic_translation"`
	Interests                []string `json:"interests"                   validate:"omitempty,max=20,dive,max=64" example:"tafsir,hadith"`
	DailyGoalMinutes         *int     `json:"daily_goal_minutes"          validate:"omitempty,min=1,max=1440" example:"15"`
	QuranTranslationSourceID *string  `json:"quran_translation_source_id" validate:"omitempty,max=255"`
	QuranRecitationID        *string  `json:"quran_recitation_id"         validate:"omitempty,max=255"`
} // @name v1.UserOnboarding

// UserPreferencesPatch stores partial preference updates.
type UserPreferencesPatch struct {
	PreferredUILang          *string   `json:"preferred_ui_lang"           validate:"omitempty,max=16"  example:"id"`
	PreferredContentLang     *string   `json:"preferred_content_lang"      validate:"omitempty,max=16"  example:"id"`
	FallbackLangs            *[]string `json:"fallback_langs"              validate:"omitempty,max=3,dive,max=16" example:"id,en"`
	ArabicLevel              *string   `json:"arabic_level"                validate:"omitempty,max=32"  example:"basic"`
	ReaderMode               *string   `json:"reader_mode"                 validate:"omitempty,max=64"  example:"arabic_translation"`
	Interests                *[]string `json:"interests"                   validate:"omitempty,max=20,dive,max=64" example:"tafsir,hadith"`
	DailyGoalMinutes         *int      `json:"daily_goal_minutes"          validate:"omitempty,min=1,max=1440" example:"15"`
	QuranTranslationSourceID *string   `json:"quran_translation_source_id" validate:"omitempty,max=255"`
	QuranRecitationID        *string   `json:"quran_recitation_id"         validate:"omitempty,max=255"`
} // @name v1.UserPreferencesPatch
