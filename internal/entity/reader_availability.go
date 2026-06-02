package entity

const (
	AvailabilityActionShowRequested    = "show_requested"
	AvailabilityActionShowArabic       = "show_arabic"
	AvailabilityActionOfferLang        = "offer_available_lang"
	AvailabilityActionHideTranslation  = "hide_translation_tab"
	AvailabilityActionHideAudio        = "hide_audio"
	AvailabilityReasonSourceLanguage   = "source_language"
	AvailabilityReasonExactAvailable   = "exact_available"
	AvailabilityReasonArabicFallback   = "arabic_fallback"
	AvailabilityReasonAlternativeLangs = "alternative_langs_available"
	AvailabilityReasonUnavailable      = "unavailable"
)

// AvailabilityDecision tells clients how to present one localized reader asset.
type AvailabilityDecision struct {
	Action         string   `json:"action"          example:"offer_available_lang"`
	Reason         string   `json:"reason"          example:"alternative_langs_available"`
	RequestedLang  string   `json:"requested_lang"  example:"en"`
	DisplayLang    string   `json:"display_lang"    example:"ar"`
	IsFallback     bool     `json:"is_fallback"     example:"true"`
	Missing        bool     `json:"missing"         example:"true"`
	AvailableLangs []string `json:"available_langs" example:"id"`
} // @name entity.AvailabilityDecision

// ReaderAvailability groups display decisions for TOC/read/section assets.
type ReaderAvailability struct {
	Title       AvailabilityDecision `json:"title"`
	Translation AvailabilityDecision `json:"translation"`
	Summary     AvailabilityDecision `json:"summary"`
	Audio       AvailabilityDecision `json:"audio"`
} // @name entity.ReaderAvailability

// NewReaderAvailability returns display decisions for the reader assets around one heading.
func NewReaderAvailability(
	requestedLang string,
	titleLang string,
	isTitleFallback bool,
	hasTranslation bool,
	translationMissing bool,
	summaryLang *string,
	hasSummary bool,
	hasAudio bool,
	availableTranslationLangs []string,
	availableSummaryLangs []string,
	availableAudioLangs []string,
) ReaderAvailability {
	return ReaderAvailability{
		Title: TitleAvailability(
			requestedLang,
			titleLang,
			isTitleFallback,
			availableTranslationLangs,
		),
		Translation: TranslationAvailability(
			requestedLang,
			hasTranslation,
			translationMissing,
			availableTranslationLangs,
		),
		Summary: SummaryAvailability(
			requestedLang,
			summaryLang,
			hasSummary,
			availableSummaryLangs,
		),
		Audio: AudioAvailability(
			requestedLang,
			hasAudio,
			availableAudioLangs,
		),
	}
}

// CatalogAvailability returns a display decision for catalog metadata.
func CatalogAvailability(
	requestedLang string,
	displayLang string,
	isFallback bool,
	availableLangs []string,
) AvailabilityDecision {
	return availabilityDecision(availabilityInput{
		requestedLang:  requestedLang,
		displayLang:    displayLang,
		isFallback:     isFallback,
		missing:        isFallback,
		availableLangs: availableLangs,
		hideAction:     AvailabilityActionShowArabic,
	})
}

// TitleAvailability returns a display decision for a translated section title.
func TitleAvailability(
	requestedLang string,
	titleLang string,
	isFallback bool,
	availableLangs []string,
) AvailabilityDecision {
	return availabilityDecision(availabilityInput{
		requestedLang:  requestedLang,
		displayLang:    titleLang,
		isFallback:     isFallback,
		missing:        isFallback,
		availableLangs: availableLangs,
		hideAction:     AvailabilityActionShowArabic,
	})
}

// TranslationAvailability returns a display decision for section translation content.
func TranslationAvailability(
	requestedLang string,
	hasTranslation bool,
	translationMissing bool,
	availableLangs []string,
) AvailabilityDecision {
	if requestedLang == "ar" {
		return AvailabilityDecision{
			Action:         AvailabilityActionHideTranslation,
			Reason:         AvailabilityReasonSourceLanguage,
			RequestedLang:  requestedLang,
			DisplayLang:    "ar",
			IsFallback:     false,
			Missing:        false,
			AvailableLangs: emptyStringSlice(availableLangs),
		}
	}

	displayLang := requestedLang
	if translationMissing || !hasTranslation {
		displayLang = "ar"
	}

	return availabilityDecision(availabilityInput{
		requestedLang:  requestedLang,
		displayLang:    displayLang,
		isFallback:     translationMissing,
		missing:        translationMissing,
		availableLangs: availableLangs,
		hideAction:     AvailabilityActionHideTranslation,
	})
}

// SummaryAvailability returns a display decision for heading summary content.
func SummaryAvailability(
	requestedLang string,
	summaryLang *string,
	hasSummary bool,
	availableLangs []string,
) AvailabilityDecision {
	displayLang := requestedLang
	if summaryLang != nil {
		displayLang = *summaryLang
	}
	if !hasSummary {
		displayLang = "ar"
	}

	isFallback := requestedLang != displayLang
	missing := !hasSummary || isFallback

	return availabilityDecision(availabilityInput{
		requestedLang:  requestedLang,
		displayLang:    displayLang,
		isFallback:     isFallback,
		missing:        missing,
		availableLangs: availableLangs,
		hideAction:     AvailabilityActionHideTranslation,
	})
}

// AudioAvailability returns a display decision for exact-language section audio.
func AudioAvailability(requestedLang string, hasAudio bool, availableLangs []string) AvailabilityDecision {
	displayLang := requestedLang
	if !hasAudio {
		displayLang = "ar"
	}

	return availabilityDecision(availabilityInput{
		requestedLang:  requestedLang,
		displayLang:    displayLang,
		isFallback:     false,
		missing:        !hasAudio,
		availableLangs: availableLangs,
		hideAction:     AvailabilityActionHideAudio,
	})
}

type availabilityInput struct {
	requestedLang  string
	displayLang    string
	isFallback     bool
	missing        bool
	availableLangs []string
	hideAction     string
}

func availabilityDecision(input availabilityInput) AvailabilityDecision {
	availableLangs := emptyStringSlice(input.availableLangs)
	decision := AvailabilityDecision{
		Action:         AvailabilityActionShowRequested,
		Reason:         AvailabilityReasonExactAvailable,
		RequestedLang:  input.requestedLang,
		DisplayLang:    input.displayLang,
		IsFallback:     input.isFallback,
		Missing:        input.missing,
		AvailableLangs: availableLangs,
	}

	if input.requestedLang == "ar" && !input.missing {
		decision.Action = AvailabilityActionShowArabic
		decision.Reason = AvailabilityReasonSourceLanguage
		return decision
	}

	if input.missing {
		if len(availableLangs) > 0 {
			decision.Action = AvailabilityActionOfferLang
			decision.Reason = AvailabilityReasonAlternativeLangs
			return decision
		}

		decision.Action = input.hideAction
		decision.Reason = AvailabilityReasonUnavailable
		return decision
	}

	if input.isFallback || input.displayLang == "ar" {
		decision.Action = AvailabilityActionShowArabic
		decision.Reason = AvailabilityReasonArabicFallback
	}

	return decision
}

func emptyStringSlice(values []string) []string {
	if values == nil {
		return []string{}
	}

	return values
}
